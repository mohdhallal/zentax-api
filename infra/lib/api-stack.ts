import * as cdk from 'aws-cdk-lib';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as ecs from 'aws-cdk-lib/aws-ecs';
import * as iam from 'aws-cdk-lib/aws-iam';
import * as kms from 'aws-cdk-lib/aws-kms';
import * as rds from 'aws-cdk-lib/aws-rds';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import * as servicediscovery from 'aws-cdk-lib/aws-servicediscovery';
import * as sns from 'aws-cdk-lib/aws-sns';
import { Construct } from 'constructs';
import { wireAlarm } from './alarms';
import { EnvConfig } from './config';
import { LogGroups, Repositories } from './data-stack';
import { makeExecutionRole, makeTaskRole } from './ecs-roles';
import { apiDbEnvironment, apiDbSecrets, apiStorageEnvironment, runtimePlatformFor } from './cluster-stack';
import { exportName } from './exports';
import { API_PORT } from './network-stack';

export interface ApiStackProps extends cdk.StackProps {
  readonly cfg: EnvConfig;
  readonly vpc: ec2.IVpc;
  readonly apiSecurityGroup: ec2.ISecurityGroup;
  /** The ECS cluster (ZenTax-<Env>-Cluster), with its default Cloud Map namespace. */
  readonly cluster: ecs.Cluster;
  /** The cluster's Cloud Map namespace name, e.g. zentax-staging-eu.local. */
  readonly namespaceName: string;
  readonly database: rds.IDatabaseInstance;
  readonly dataKey: kms.IKey;
  readonly appDbSecret: secretsmanager.ISecret;
  readonly authEncryptionKeySecret: secretsmanager.ISecret;
  readonly documentsBucket: s3.IBucket;
  readonly repositories: Repositories;
  readonly logGroups: LogGroups;
  /** The environment's alarm topic (Data stack). */
  readonly alarmTopic: sns.ITopic;
}

/** The Cloud Map name the api service registers under; the web tier's GO_API_URL is built from it. */
export const API_SERVICE_DISCOVERY_NAME = 'api';

/** `http://api.zentax-<env>.local:3000` — what the web tier's GO_API_URL is. */
export function apiInternalUrl(namespaceName: string): string {
  return `http://${API_SERVICE_DISCOVERY_NAME}.${namespaceName}:${API_PORT}`;
}

/**
 * The product's public origin — `PUBLIC_BASE_URL=https://<publicHostname>`,
 * the Go config's `app.publicBaseUrl`: the base for the absolute links the api
 * will hand out. Nothing consumes it yet (the invite link is built by the UI
 * from its own origin; the api-side link arrives with e-mail delivery). Only
 * the api service gets it: the seed CLI shares the loader but builds no links,
 * and the value is omitted entirely when no public hostname is configured.
 */
export function apiPublicEnvironment(cfg: EnvConfig): Record<string, string> {
  return cfg.publicHostname ? { PUBLIC_BASE_URL: `https://${cfg.publicHostname}` } : {};
}

/**
 * Whose `X-Forwarded-For` the api believes — `RATE_LIMIT_TRUSTED_PROXIES`, the
 * Go config's `rateLimit.trustedProxies`.
 *
 * The api's default is to trust nothing and charge every request to its socket
 * peer, because a broad private-range default turned out to be a bypass: any
 * caller that could reach the port was inside the trusted range and had its own
 * forged header believed. Here the cell names its own VPC, which is exactly the
 * set of hops in front of the api: the ALB, which APPENDS the real client
 * address on the right of anything the caller wrote, and the web tasks. So a
 * per-browser budget keys per browser, and a header forged from the internet is
 * ignored because its packets arrive from outside this range.
 */
export function apiTrustedProxyEnvironment(cfg: EnvConfig): Record<string, string> {
  return { RATE_LIMIT_TRUSTED_PROXIES: cfg.cidr };
}

/**
 * ZenTax-<Env>-Api: the Go api as a Fargate service on the Cluster stack's
 * cluster, registered as `api` in its Cloud Map namespace, with its task and
 * execution roles, the documents-bucket / KMS grants and its running-task
 * alarm. It has no public surface: only the web tasks' security group may
 * reach it (Network stack), and the UI app's Web stack finds it by the
 * `api-internal-url` export (lib/exports.ts).
 */
export class ApiStack extends cdk.Stack {
  public readonly cluster: ecs.ICluster;
  public readonly service: ecs.FargateService;
  public readonly taskDefinition: ecs.FargateTaskDefinition;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);
    const { cfg } = props;
    this.cluster = props.cluster;

    // ---- roles + data-plane grants ---------------------------------------------
    const apiTaskRole = makeTaskRole(this, cfg, 'api');
    // Explicit object-level verbs only: the api reads, writes, deletes and
    // aborts uploads of *current* objects. No `*Version` action — the bucket's
    // versioning is the undo log, and nothing the api does may erase it.
    apiTaskRole.addToPolicy(new iam.PolicyStatement({
      sid: 'DocumentsObjects',
      actions: ['s3:GetObject', 's3:PutObject', 's3:DeleteObject', 's3:AbortMultipartUpload'],
      resources: [props.documentsBucket.arnForObjects('*')],
    }));
    apiTaskRole.addToPolicy(new iam.PolicyStatement({
      sid: 'DocumentsList',
      actions: ['s3:ListBucket'],
      resources: [props.documentsBucket.bucketArn],
    }));
    props.dataKey.grantEncryptDecrypt(apiTaskRole);

    // ---- task definition ----------------------------------------------------------
    this.taskDefinition = new ecs.FargateTaskDefinition(this, 'ApiTask', {
      family: `zentax-${cfg.name}-api`,
      cpu: 512,
      memoryLimitMiB: 1024,
      runtimePlatform: runtimePlatformFor(cfg),
      taskRole: apiTaskRole,
      executionRole: makeExecutionRole(this, cfg, 'api'),
    });
    this.taskDefinition.addContainer('api', {
      containerName: 'api',
      image: ecs.ContainerImage.fromEcrRepository(props.repositories.api, cfg.imageTag),
      portMappings: [{ containerPort: API_PORT, protocol: ecs.Protocol.TCP }],
      environment: {
        ...apiDbEnvironment(cfg, props.database),
        // The public origin (CloudFront / custom domain) is the UI app's; it is
        // configured, not referenced — see EnvConfig.corsAllowedOrigins.
        ...apiStorageEnvironment(cfg, props.documentsBucket, cfg.corsAllowedOrigins),
        ...apiPublicEnvironment(cfg),
        ...apiTrustedProxyEnvironment(cfg),
      },
      secrets: apiDbSecrets(props.appDbSecret, props.authEncryptionKeySecret),
      logging: ecs.LogDrivers.awsLogs({ logGroup: props.logGroups.api, streamPrefix: 'api' }),
      healthCheck: {
        command: ['CMD-SHELL', `wget -qO /dev/null http://127.0.0.1:${API_PORT}/health || exit 1`],
        interval: cdk.Duration.seconds(15),
        timeout: cdk.Duration.seconds(5),
        startPeriod: cdk.Duration.seconds(30),
        retries: 3,
      },
      readonlyRootFilesystem: false,
      stopTimeout: cdk.Duration.seconds(30),
    });

    // ---- service ----------------------------------------------------------------
    this.service = new ecs.FargateService(this, 'ApiService', {
      serviceName: `zentax-${cfg.name}-api`,
      cluster: this.cluster,
      taskDefinition: this.taskDefinition,
      desiredCount: cfg.apiDesiredCount,
      assignPublicIp: false,
      vpcSubnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS },
      securityGroups: [props.apiSecurityGroup],
      circuitBreaker: { enable: true, rollback: true },
      minHealthyPercent: 100,
      maxHealthyPercent: 200,
      enableExecuteCommand: false,
      cloudMapOptions: {
        name: API_SERVICE_DISCOVERY_NAME,
        dnsRecordType: servicediscovery.DnsRecordType.A,
        dnsTtl: cdk.Duration.seconds(10),
        container: this.taskDefinition.defaultContainer,
        containerPort: API_PORT,
      },
    });
    const desired = cfg.apiDesiredCount;
    const scaling = this.service.autoScaleTaskCount({ minCapacity: desired, maxCapacity: Math.max(2 * desired, desired + 1) });
    scaling.scaleOnCpuUtilization('Cpu60', {
      targetUtilizationPercent: 60,
      scaleInCooldown: cdk.Duration.minutes(5),
      scaleOutCooldown: cdk.Duration.minutes(1),
    });

    // ---- Alarm: Container Insights' RunningTaskCount — a task-count floor the
    // service's own metrics cannot express. 1-minute periods, 5 in a row.
    wireAlarm(new cloudwatch.Alarm(this, 'apiRunningTasksAlarm', {
      alarmName: `zentax-${cfg.name}-api-running-tasks`,
      alarmDescription: `ZenTax ${cfg.name}: api has fewer than ${desired} running task(s) for 5 minutes (Container Insights)`,
      metric: new cloudwatch.Metric({
        namespace: 'ECS/ContainerInsights',
        metricName: 'RunningTaskCount',
        dimensionsMap: { ClusterName: this.cluster.clusterName, ServiceName: this.service.serviceName },
        statistic: 'Average',
        period: cdk.Duration.minutes(1),
      }),
      threshold: desired,
      comparisonOperator: cloudwatch.ComparisonOperator.LESS_THAN_THRESHOLD,
      evaluationPeriods: 5,
      // No datapoint means no task is reporting at all — that is the outage.
      treatMissingData: cloudwatch.TreatMissingData.BREACHING,
    }), props.alarmTopic);

    // ---- Outputs: the export contract (lib/exports.ts) -----------------------------
    const out = (id: string, key: Parameters<typeof exportName>[1], value: string, description?: string) =>
      new cdk.CfnOutput(this, id, { value, description, exportName: exportName(cfg.name, key) });
    out('ApiServiceName', 'api-service-name', this.service.serviceName);
    out('ApiInternalUrl', 'api-internal-url', apiInternalUrl(props.namespaceName), 'What the web tier sets as GO_API_URL');
    out('ApiTaskDefinitionArn', 'api-task-arn', this.taskDefinition.taskDefinitionArn, 'The revision CDK registered');
    new cdk.CfnOutput(this, 'ApiTaskDefinitionFamily', { value: this.taskDefinition.family });
  }
}
