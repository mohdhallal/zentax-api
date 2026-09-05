import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as rds from 'aws-cdk-lib/aws-rds';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import * as servicediscovery from 'aws-cdk-lib/aws-servicediscovery';
import { Construct } from 'constructs';
import { EnvConfig } from './config';
import { APP_DB_USER, DB_NAME, LogGroups, Repositories } from './data-stack';
import { makeExecutionRole, makeTaskRole } from './ecs-roles';
import { exportName } from './exports';

export interface ClusterStackProps extends cdk.StackProps {
  readonly cfg: EnvConfig;
  readonly vpc: ec2.IVpc;
  readonly database: rds.IDatabaseInstance;
  readonly masterSecret: secretsmanager.ISecret;
  readonly appDbSecret: secretsmanager.ISecret;
  readonly authEncryptionKeySecret: secretsmanager.ISecret;
  /** Generated initial admin password, injected into the seed task as SEED_ADMIN_PASSWORD. */
  readonly seedAdminSecret: secretsmanager.ISecret;
  readonly documentsBucket: s3.IBucket;
  readonly repositories: Repositories;
  readonly logGroups: LogGroups;
}

/** Environment the api and the seed CLI share (the DB contract). */
export function apiDbEnvironment(cfg: EnvConfig, database: rds.IDatabaseInstance): Record<string, string> {
  return {
    APP_ENV: cfg.name,
    DB_HOST: database.dbInstanceEndpointAddress,
    DB_PORT: database.dbInstanceEndpointPort,
    DB_NAME: DB_NAME,
    DB_USER: APP_DB_USER,
    DB_SSLMODE: 'require',
    LOG_FORMAT: 'json',
  };
}

/**
 * The api's non-secret config beyond the DB block. The Go config loader
 * validates all of it at start-up, so the seed CLI (same binary, same loader)
 * needs it too even though it serves no HTTP and touches no object.
 */
export function apiStorageEnvironment(cfg: EnvConfig, documentsBucket: s3.IBucket, corsOrigin: string): Record<string, string> {
  return {
    CORS_ALLOWED_ORIGINS: corsOrigin,
    STORAGE_DRIVER: 's3',
    STORAGE_S3_BUCKET: documentsBucket.bucketName,
    STORAGE_S3_REGION: cfg.region,
  };
}

export function apiDbSecrets(appDbSecret: secretsmanager.ISecret, authKey: secretsmanager.ISecret): Record<string, ecs.Secret> {
  return {
    DB_PASSWORD: ecs.Secret.fromSecretsManager(appDbSecret, 'password'),
    AUTH_ENCRYPTION_KEY: ecs.Secret.fromSecretsManager(authKey),
  };
}

export function runtimePlatformFor(cfg: EnvConfig): ecs.RuntimePlatform {
  return {
    cpuArchitecture: cfg.cpuArchitecture === 'ARM64' ? ecs.CpuArchitecture.ARM64 : ecs.CpuArchitecture.X86_64,
    operatingSystemFamily: ecs.OperatingSystemFamily.LINUX,
  };
}

/**
 * ZenTax-<Env>-Cluster: the ECS cluster + Cloud Map namespace and the one-off
 * `migrate` / `seed` task definitions, run with `aws ecs run-task`.
 *
 * They sit in their own stack between Data and Api because the api cannot boot
 * before the first migration has created the `zentax_app` role it connects as:
 * migrations must be runnable before the Api stack (whose service would
 * otherwise crash-loop into a circuit-breaker rollback) exists. That is also
 * the natural CI order — migrate, then roll the service.
 *
 * The UI app's Web stack places its service on this cluster through the
 * cluster/namespace exports (lib/exports.ts).
 */
export class ClusterStack extends cdk.Stack {
  public readonly cluster: ecs.Cluster;
  public readonly namespaceName: string;
  public readonly migrateTaskDefinition: ecs.FargateTaskDefinition;
  public readonly seedTaskDefinition: ecs.FargateTaskDefinition;

  constructor(scope: Construct, id: string, props: ClusterStackProps) {
    super(scope, id, props);
    const { cfg, vpc } = props;
    const runtimePlatform = runtimePlatformFor(cfg);

    // ---- Cluster + service discovery -----------------------------------------
    this.namespaceName = `zentax-${cfg.name}.local`;
    this.cluster = new ecs.Cluster(this, 'Cluster', {
      clusterName: `zentax-${cfg.name}`,
      vpc,
      containerInsightsV2: ecs.ContainerInsights.ENABLED,
      enableFargateCapacityProviders: true,
      defaultCloudMapNamespace: {
        name: this.namespaceName,
        type: servicediscovery.NamespaceType.DNS_PRIVATE,
        vpc,
        useForServiceConnect: false,
      },
    });
    const namespace = this.cluster.defaultCloudMapNamespace;
    if (!namespace) throw new Error('the cluster has no default Cloud Map namespace');

    // ---- migrate: migrations/deploy/*.sql as the master user, then provisions zentax_app ----
    this.migrateTaskDefinition = new ecs.FargateTaskDefinition(this, 'MigrateTask', {
      family: `zentax-${cfg.name}-migrate`,
      cpu: 256,
      memoryLimitMiB: 512,
      runtimePlatform,
      taskRole: makeTaskRole(this, cfg, 'migrate'),
      executionRole: makeExecutionRole(this, cfg, 'migrate'),
    });
    this.migrateTaskDefinition.addContainer('migrate', {
      containerName: 'migrate',
      image: ecs.ContainerImage.fromEcrRepository(props.repositories.migrate, cfg.imageTag),
      environment: {
        PGHOST: props.database.dbInstanceEndpointAddress,
        PGPORT: props.database.dbInstanceEndpointPort,
        PGDATABASE: DB_NAME,
        PGSSLMODE: 'require',
        APP_DB_USER: APP_DB_USER,
      },
      secrets: {
        PGUSER: ecs.Secret.fromSecretsManager(props.masterSecret, 'username'),
        PGPASSWORD: ecs.Secret.fromSecretsManager(props.masterSecret, 'password'),
        APP_DB_PASSWORD: ecs.Secret.fromSecretsManager(props.appDbSecret, 'password'),
      },
      logging: ecs.LogDrivers.awsLogs({ logGroup: props.logGroups.migrate, streamPrefix: 'migrate' }),
    });

    // ---- seed: first tenant + admin, from the api image ----------------------
    this.seedTaskDefinition = new ecs.FargateTaskDefinition(this, 'SeedTask', {
      family: `zentax-${cfg.name}-seed`,
      cpu: 256,
      memoryLimitMiB: 512,
      runtimePlatform,
      taskRole: makeTaskRole(this, cfg, 'seed'),
      executionRole: makeExecutionRole(this, cfg, 'seed'),
    });
    this.seedTaskDefinition.addContainer('seed', {
      containerName: 'seed',
      image: ecs.ContainerImage.fromEcrRepository(props.repositories.api, cfg.imageTag),
      // The arguments (--tenant-slug, --email, --timezone) are supplied at run
      // time via `ecs run-task --overrides` (see README). The password comes
      // from the zentax/<env>/seed-admin secret (SEED_ADMIN_PASSWORD, read by
      // seed-admin when --password is absent): it must never sit in a task
      // definition, a template, or a workflow log.
      entryPoint: ['seed-admin'],
      command: ['--help'],
      environment: {
        ...apiDbEnvironment(cfg, props.database),
        // seed-admin serves no HTTP; the placeholder only satisfies config validation.
        ...apiStorageEnvironment(cfg, props.documentsBucket, 'https://seed.invalid'),
      },
      secrets: {
        ...apiDbSecrets(props.appDbSecret, props.authEncryptionKeySecret),
        SEED_ADMIN_PASSWORD: ecs.Secret.fromSecretsManager(props.seedAdminSecret),
      },
      logging: ecs.LogDrivers.awsLogs({ logGroup: props.logGroups.seed, streamPrefix: 'seed' }),
    });

    // ---- Outputs: the export contract (UI app's Web stack + CI run-task) ------
    const out = (id: string, key: Parameters<typeof exportName>[1], value: string, description?: string) =>
      new cdk.CfnOutput(this, id, { value, description, exportName: exportName(cfg.name, key) });
    out('ClusterName', 'cluster-name', this.cluster.clusterName);
    out('ClusterArn', 'cluster-arn', this.cluster.clusterArn);
    out('NamespaceName', 'namespace-name', namespace.namespaceName, 'Cloud Map private DNS namespace (api.<namespace>)');
    out('NamespaceId', 'namespace-id', namespace.namespaceId);
    out('NamespaceArn', 'namespace-arn', namespace.namespaceArn);
    out('MigrateTaskDefinitionFamily', 'migrate-task-family', this.migrateTaskDefinition.family,
      'Pass to `ecs run-task --task-definition` (latest revision)');
    out('MigrateTaskDefinitionArn', 'migrate-task-arn', this.migrateTaskDefinition.taskDefinitionArn,
      'The revision CDK registered — what the pipeline runs');
    out('SeedTaskDefinitionFamily', 'seed-task-family', this.seedTaskDefinition.family,
      'Pass to `ecs run-task --task-definition` (latest revision)');
    out('SeedTaskDefinitionArn', 'seed-task-arn', this.seedTaskDefinition.taskDefinitionArn,
      'The revision CDK registered — what the pipeline runs');
    out('SeedAdminSecretArn', 'seed-admin-secret-arn', props.seedAdminSecret.secretArn,
      'Initial admin password the seed task uses (SEED_ADMIN_PASSWORD); read it once, then rotate');
  }
}
