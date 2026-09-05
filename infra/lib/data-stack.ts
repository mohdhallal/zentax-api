import * as cdk from 'aws-cdk-lib';
import * as cloudwatch from 'aws-cdk-lib/aws-cloudwatch';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as ecr from 'aws-cdk-lib/aws-ecr';
import * as kms from 'aws-cdk-lib/aws-kms';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as rds from 'aws-cdk-lib/aws-rds';
import * as s3 from 'aws-cdk-lib/aws-s3';
import * as secretsmanager from 'aws-cdk-lib/aws-secretsmanager';
import * as sns from 'aws-cdk-lib/aws-sns';
import { Construct } from 'constructs';
import { alarmTopic, postgresMaxConnections, wireAlarm } from './alarms';
import { EnvConfig } from './config';
import { exportName } from './exports';
import { DB_PORT } from './network-stack';

export interface DataStackProps extends cdk.StackProps {
  readonly cfg: EnvConfig;
  readonly vpc: ec2.IVpc;
  readonly dbSecurityGroup: ec2.ISecurityGroup;
}

export const DB_NAME = 'zentax';
export const APP_DB_USER = 'zentax_app';

/** RDS master/app passwords: avoid the characters RDS rejects ("/", "@", '"', space). */
const PASSWORD_EXCLUDE = ' /@"\'\\`$&%<>;|{}[]()*?!#^~=+,:';

/** The log groups of the API-owned containers (the web log group is the UI app's). */
export interface LogGroups {
  readonly api: logs.LogGroup;
  readonly migrate: logs.LogGroup;
  readonly seed: logs.LogGroup;
}

/** The API-owned image repositories (zentax/<env>/web is the UI app's). */
export interface Repositories {
  readonly api: ecr.Repository;
  readonly migrate: ecr.Repository;
}

/**
 * ZenTax-<Env>-Data: everything that holds state or that a deployment must
 * never accidentally delete — KMS key, RDS, secrets, the documents bucket,
 * the api/migrate ECR registries and the log groups.
 *
 * The web tier's state (its ECR repository, its log group, the origin-verify
 * secret CloudFront and the ALB share) lives in the UI app's Web stack.
 */
export class DataStack extends cdk.Stack {
  public readonly dataKey: kms.Key;
  public readonly database: rds.DatabaseInstance;
  /** Master credentials {username, password} — used by the migrate task only. */
  public readonly masterSecret: secretsmanager.ISecret;
  /** App role credentials {username: zentax_app, password} — api + seed + migrate(APP_DB_PASSWORD). */
  public readonly appDbSecret: secretsmanager.Secret;
  /** Plain 64-char passphrase for AUTH_ENCRYPTION_KEY. */
  public readonly authEncryptionKeySecret: secretsmanager.Secret;
  /** Generated initial password of the first admin (seed task: SEED_ADMIN_PASSWORD). */
  public readonly seedAdminSecret: secretsmanager.Secret;
  public readonly documentsBucket: s3.Bucket;
  public readonly repositories: Repositories;
  public readonly logGroups: LogGroups;
  /** The environment's alarm topic (eu-central-1); Api and the UI app's Web stack reuse it. */
  public readonly alarmTopic: sns.Topic;

  constructor(scope: Construct, id: string, props: DataStackProps) {
    super(scope, id, props);
    const { cfg, vpc } = props;
    const removal = cfg.dataRemovalPolicy;

    // ---- Alarms: one topic per environment, e-mail if configured ------------
    this.alarmTopic = alarmTopic(this, cfg, 'Alarms', `zentax-${cfg.name}-alarms`);

    // ---- KMS -----------------------------------------------------------------
    this.dataKey = new kms.Key(this, 'DataKey', {
      alias: `zentax/${cfg.name}/data`,
      description: `ZenTax ${cfg.name}: RDS storage, documents bucket, Performance Insights`,
      enableKeyRotation: true,
      removalPolicy: removal,
      pendingWindow: cdk.Duration.days(30),
    });

    // ---- Secrets -------------------------------------------------------------
    this.appDbSecret = new secretsmanager.Secret(this, 'AppDbSecret', {
      secretName: `zentax/${cfg.name}/app-db`,
      description: `ZenTax ${cfg.name}: Postgres application role (non-superuser, non-BYPASSRLS)`,
      generateSecretString: {
        secretStringTemplate: JSON.stringify({ username: APP_DB_USER }),
        generateStringKey: 'password',
        passwordLength: 40,
        excludePunctuation: true,
        excludeCharacters: PASSWORD_EXCLUDE,
      },
      removalPolicy: removal,
    });
    this.authEncryptionKeySecret = new secretsmanager.Secret(this, 'AuthEncryptionKeySecret', {
      secretName: `zentax/${cfg.name}/auth-encryption-key`,
      description: `ZenTax ${cfg.name}: AUTH_ENCRYPTION_KEY passphrase for the api`,
      generateSecretString: { passwordLength: 64, excludePunctuation: true },
      removalPolicy: removal,
    });
    // The seed task reads this as SEED_ADMIN_PASSWORD when no --password
    // override is given, so the initial admin password never travels through a
    // shell, a workflow log or a task definition. Rotate/remove it after the
    // first login.
    this.seedAdminSecret = new secretsmanager.Secret(this, 'SeedAdminSecret', {
      secretName: `zentax/${cfg.name}/seed-admin`,
      description: `ZenTax ${cfg.name}: initial password of the first admin user created by the seed task`,
      generateSecretString: { passwordLength: 32, excludePunctuation: true },
      removalPolicy: removal,
    });

    // ---- RDS PostgreSQL 16 ---------------------------------------------------
    const engine = rds.DatabaseInstanceEngine.postgres({ version: rds.PostgresEngineVersion.VER_16 });
    const parameterGroup = new rds.ParameterGroup(this, 'DbParams', {
      engine,
      description: `ZenTax ${cfg.name}: postgres16, TLS enforced`,
      parameters: {
        'rds.force_ssl': '1',
        'log_min_duration_statement': '1000',
        'log_connections': '1',
        'log_disconnections': '1',
        // Never log bind parameters (tax data, credentials) — neither for slow
        // statements nor for statements that raised an error — so no PII/secret
        // can reach the CloudWatch export.
        'log_parameter_max_length': '0',
        'log_parameter_max_length_on_error': '0',
      },
    });
    const instanceIdentifier = `zentax-${cfg.name}`;
    // Pre-create the export log group so retention is enforced without a
    // log-retention Lambda (RDS reuses an existing /aws/rds/instance/... group).
    const dbLogGroup = new logs.LogGroup(this, 'DbLogGroup', {
      logGroupName: `/aws/rds/instance/${instanceIdentifier}/postgresql`,
      retention: cfg.logRetentionDays,
      removalPolicy: removal,
    });
    // Enhanced monitoring writes to the account+region-wide `RDSOSMetrics`
    // group; if it exists it is reused, so pre-creating it is the only way to
    // give it a retention. Both environments share one account+region today,
    // hence exactly one of them owns it (context ownsRdsOsMetricsLogGroup).
    let rdsOsMetricsLogGroup: logs.LogGroup | undefined;
    if (cfg.ownsRdsOsMetricsLogGroup) {
      rdsOsMetricsLogGroup = new logs.LogGroup(this, 'RdsOsMetricsLogGroup', {
        logGroupName: 'RDSOSMetrics',
        retention: cfg.logRetentionDays,
        removalPolicy: removal,
      });
    }

    this.database = new rds.DatabaseInstance(this, 'Database', {
      instanceIdentifier,
      engine,
      instanceType: new ec2.InstanceType(cfg.dbInstanceClass),
      vpc,
      vpcSubnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS },
      securityGroups: [props.dbSecurityGroup],
      port: DB_PORT,
      databaseName: DB_NAME,
      credentials: rds.Credentials.fromGeneratedSecret('zentax_master', {
        secretName: `zentax/${cfg.name}/db-master`,
        excludeCharacters: PASSWORD_EXCLUDE,
      }),
      multiAz: cfg.dbMultiAz,
      allocatedStorage: cfg.dbAllocatedStorageGb,
      maxAllocatedStorage: cfg.dbMaxAllocatedStorageGb,
      storageType: rds.StorageType.GP3,
      storageEncrypted: true,
      storageEncryptionKey: this.dataKey,
      backupRetention: cdk.Duration.days(cfg.dbBackupDays),
      preferredBackupWindow: '02:00-03:00',
      preferredMaintenanceWindow: 'Sun:03:30-Sun:04:30',
      copyTagsToSnapshot: true,
      // Three independent guards (ADR-0024):
      //  - deletionProtection: the API-level guard against `delete-db-instance`
      //    and stack deletion while it is on;
      //  - removalPolicy: what CloudFormation does once a delete is allowed —
      //    a final snapshot where data is RETAINed (production), plain delete
      //    where it is not (staging);
      //  - deleteAutomatedBackups: only staging throws its PITR history away.
      deleteAutomatedBackups: removal === cdk.RemovalPolicy.DESTROY,
      deletionProtection: cfg.deletionProtection,
      removalPolicy: removal === cdk.RemovalPolicy.RETAIN ? cdk.RemovalPolicy.SNAPSHOT : cdk.RemovalPolicy.DESTROY,
      enablePerformanceInsights: true,
      performanceInsightRetention: rds.PerformanceInsightRetention.DEFAULT,
      performanceInsightEncryptionKey: this.dataKey,
      monitoringInterval: cdk.Duration.seconds(60),
      cloudwatchLogsExports: ['postgresql'],
      parameterGroup,
      autoMinorVersionUpgrade: true,
      allowMajorVersionUpgrade: false,
      publiclyAccessible: false,
      caCertificate: rds.CaCertificate.RDS_CA_RSA2048_G1,
      iamAuthentication: false,
    });
    this.database.node.addDependency(dbLogGroup);
    if (rdsOsMetricsLogGroup) this.database.node.addDependency(rdsOsMetricsLogGroup);
    if (!this.database.secret) throw new Error('RDS master secret was not generated');
    this.masterSecret = this.database.secret;
    // The generated master secret follows the environment's data policy like
    // every other secret (production: RETAIN — a restored snapshot needs it).
    // `database.secret` is the *attachment* view; the policy belongs on the
    // underlying AWS::SecretsManager::Secret the instance construct created.
    const masterSecretResource = this.database.node.findChild('Secret').node.defaultChild as cdk.CfnResource;
    masterSecretResource.applyRemovalPolicy(removal);

    // ---- RDS alarms ----------------------------------------------------------
    const fiveMinutes = cdk.Duration.minutes(5);
    const dbMetric = (name: string, statistic = 'Average') =>
      this.database.metric(name, { period: fiveMinutes, statistic });
    const allocatedBytes = cfg.dbAllocatedStorageGb * 1024 ** 3;
    const { maxConnections, memoryBytes } = postgresMaxConnections(cfg.dbInstanceClass);
    const dbAlarm = (id: string, props: cloudwatch.AlarmProps) =>
      wireAlarm(new cloudwatch.Alarm(this, id, props), this.alarmTopic);
    dbAlarm('DbCpuAlarm', {
      alarmName: `zentax-${cfg.name}-rds-cpu`,
      alarmDescription: `ZenTax ${cfg.name}: RDS CPU above 80 % for 5 minutes`,
      metric: dbMetric('CPUUtilization'),
      threshold: 80,
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
      evaluationPeriods: 1,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });
    dbAlarm('DbFreeStorageAlarm', {
      alarmName: `zentax-${cfg.name}-rds-free-storage`,
      alarmDescription: `ZenTax ${cfg.name}: RDS free storage below 10 % of the ${cfg.dbAllocatedStorageGb} GB initially allocated (storage autoscaling may already have grown it)`,
      metric: dbMetric('FreeStorageSpace', 'Minimum'),
      threshold: Math.floor(allocatedBytes * 0.1),
      comparisonOperator: cloudwatch.ComparisonOperator.LESS_THAN_THRESHOLD,
      evaluationPeriods: 1,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });
    dbAlarm('DbConnectionsAlarm', {
      alarmName: `zentax-${cfg.name}-rds-connections`,
      alarmDescription: `ZenTax ${cfg.name}: RDS connections above 80 % of the assumed max_connections=${maxConnections} for db.${cfg.dbInstanceClass} (RDS default LEAST(DBInstanceClassMemory/9531392, 5000))`,
      metric: dbMetric('DatabaseConnections', 'Maximum'),
      threshold: Math.floor(maxConnections * 0.8),
      comparisonOperator: cloudwatch.ComparisonOperator.GREATER_THAN_THRESHOLD,
      evaluationPeriods: 1,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });
    dbAlarm('DbFreeableMemoryAlarm', {
      alarmName: `zentax-${cfg.name}-rds-freeable-memory`,
      alarmDescription: `ZenTax ${cfg.name}: RDS freeable memory below 10 % of the ${memoryBytes / 1024 ** 3} GiB of db.${cfg.dbInstanceClass}`,
      metric: dbMetric('FreeableMemory', 'Minimum'),
      threshold: Math.floor(memoryBytes * 0.1),
      comparisonOperator: cloudwatch.ComparisonOperator.LESS_THAN_THRESHOLD,
      evaluationPeriods: 1,
      treatMissingData: cloudwatch.TreatMissingData.NOT_BREACHING,
    });

    // ---- Documents bucket (ADR-0022) -----------------------------------------
    this.documentsBucket = new s3.Bucket(this, 'DocumentsBucket', {
      bucketName: `zentax-${cfg.name}-documents-${cfg.account}-${cfg.region}`,
      encryption: s3.BucketEncryption.KMS,
      encryptionKey: this.dataKey,
      bucketKeyEnabled: true,
      versioned: true,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      objectOwnership: s3.ObjectOwnership.BUCKET_OWNER_ENFORCED,
      enforceSSL: true,
      minimumTLSVersion: 1.2,
      lifecycleRules: [
        // Overwritten/deleted document versions live exactly as long as the
        // database can be restored (dbBackupDays): one retention window for the
        // whole restore story (ADR-0007, ADR-0014).
        { id: 'expire-noncurrent-versions', noncurrentVersionExpiration: cdk.Duration.days(cfg.dbBackupDays), enabled: true },
        { id: 'abort-incomplete-multipart', abortIncompleteMultipartUploadAfter: cdk.Duration.days(7), enabled: true },
      ],
      removalPolicy: removal,
      autoDeleteObjects: removal === cdk.RemovalPolicy.DESTROY,
    });

    // ---- ECR -----------------------------------------------------------------
    // Repository names are per environment (zentax/<env>/api ...): both
    // environments live in one account+region today, and ECR names must be
    // unique there. It also gives each deploy role a clean push boundary
    // (zentax/<env>/web is created by the UI app's Web stack, same scheme).
    const makeRepo = (id: string, component: string) =>
      new ecr.Repository(this, id, {
        repositoryName: `zentax/${cfg.name}/${component}`,
        imageScanOnPush: true,
        imageTagMutability: ecr.TagMutability.MUTABLE,
        emptyOnDelete: removal === cdk.RemovalPolicy.DESTROY,
        removalPolicy: removal,
        lifecycleRules: [
          { rulePriority: 1, description: 'expire untagged after 7 days', tagStatus: ecr.TagStatus.UNTAGGED, maxImageAge: cdk.Duration.days(7) },
          { rulePriority: 2, description: 'keep the last 30 tagged images', tagStatus: ecr.TagStatus.TAGGED, tagPatternList: ['*'], maxImageCount: 30 },
        ],
      });
    this.repositories = {
      api: makeRepo('ApiRepo', 'api'),
      migrate: makeRepo('MigrateRepo', 'migrate'),
    };

    // ---- Log groups ----------------------------------------------------------
    const makeLogGroup = (id: string, component: string) =>
      new logs.LogGroup(this, id, {
        logGroupName: `/zentax/${cfg.name}/${component}`,
        retention: cfg.logRetentionDays,
        removalPolicy: removal,
      });
    this.logGroups = {
      api: makeLogGroup('ApiLogGroup', 'api'),
      migrate: makeLogGroup('MigrateLogGroup', 'migrate'),
      seed: makeLogGroup('SeedLogGroup', 'seed'),
    };

    // ---- Outputs -------------------------------------------------------------
    new cdk.CfnOutput(this, 'DbEndpoint', { value: this.database.dbInstanceEndpointAddress });
    new cdk.CfnOutput(this, 'DataKeyArn', { value: this.dataKey.keyArn });
    new cdk.CfnOutput(this, 'MasterSecretArn', { value: this.masterSecret.secretArn });
    new cdk.CfnOutput(this, 'AppDbSecretArn', { value: this.appDbSecret.secretArn });
    new cdk.CfnOutput(this, 'AuthEncryptionKeySecretArn', { value: this.authEncryptionKeySecret.secretArn });
    // The export contract (lib/exports.ts): the UI app's Web stack imports the
    // topic; the pipelines read the bucket and repository URIs.
    new cdk.CfnOutput(this, 'AlarmTopicArn', { value: this.alarmTopic.topicArn, exportName: exportName(cfg.name, 'alarm-topic-arn') });
    new cdk.CfnOutput(this, 'DocumentsBucketName', { value: this.documentsBucket.bucketName, exportName: exportName(cfg.name, 'documents-bucket') });
    new cdk.CfnOutput(this, 'ApiRepositoryUri', { value: this.repositories.api.repositoryUri, exportName: exportName(cfg.name, 'ecr-api') });
    new cdk.CfnOutput(this, 'MigrateRepositoryUri', { value: this.repositories.migrate.repositoryUri, exportName: exportName(cfg.name, 'ecr-migrate') });
  }
}
