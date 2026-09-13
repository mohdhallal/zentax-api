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
import * as ses from 'aws-cdk-lib/aws-ses';
import * as sns from 'aws-cdk-lib/aws-sns';
import { Construct } from 'constructs';
import { wireAlarm } from './alarms';
import { EnvConfig, mailConfigurationSetName } from './config';
import { LogGroups, Repositories } from './data-stack';
import { makeExecutionRole, makeTaskRole } from './ecs-roles';
import { apiAuditExportEnvironment, apiDbEnvironment, apiDbSecrets, apiMailEnvironment, apiStorageEnvironment, runtimePlatformFor } from './cluster-stack';
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
  /** WORM audit archive (ADR-0008): Object Lock, COMPLIANCE mode (Data stack). */
  readonly auditExportBucket: s3.IBucket;
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
 * The header CloudFront writes the viewer's address and source port into
 * ("198.51.100.9:53100"). Sent to the origin only when the distribution's
 * origin-request policy forwards CloudFront's OWN headers — see
 * apiTrustedProxyEnvironment.
 */
export const CLOUDFRONT_VIEWER_ADDRESS_HEADER = 'CloudFront-Viewer-Address';

/**
 * WHO THE API BELIEVES A REQUEST IS FROM — `RATE_LIMIT_TRUSTED_PROXIES` and
 * `RATE_LIMIT_EDGE_VIEWER_HEADER`, the Go config's `rateLimit.trustedProxies`
 * and `rateLimit.edgeViewerHeader`. One answer, used twice: it keys the rate
 * limiter's per-client budget, and it is the "from where" on every row of the
 * security-event stream (ADR-0008 stream 2). That second use is why this is
 * worth the comment.
 *
 * The api's default is to trust nothing and charge every request to its socket
 * peer, because a broad private-range default turned out to be a bypass: any
 * caller that could reach the port was inside the trusted range and had its own
 * forged header believed.
 *
 * 1. THE TRUSTED SET IS THE TIER IN FRONT, NOT THE WHOLE CELL. It used to be
 *    `cfg.cidr`, which is not "the hops in front of the api" — it is everything
 *    in the cell: every task, every sidecar, every one-off job. Trust is by
 *    network range and nothing else, so anything inside the range can write any
 *    client address into a header, have it believed, and have it written into
 *    an append-only evidence store. It is now the PRIVATE subnets, where the
 *    web tasks run — the api's only permitted socket peers (Network stack).
 *    The public subnets, which hold the internet-facing ALB and the NAT
 *    gateways, are out of it. What keeps the boundary honest is the security
 *    group rather than this list: only the web tier's group may reach
 *    API_PORT, so a forger has to already be running as the tier that is
 *    legitimately in front.
 *
 * 2. THE DISTRIBUTION IS A HOP THE CHAIN WALK CANNOT SEE PAST. A cell is
 *    reached CloudFront -> ALB -> web -> api, so the forwarded chain arriving
 *    at the api ends "…, viewer, cloudFrontEdge, alb". Walked from the right it
 *    skips our own ALB and stops at a CLOUDFRONT EDGE SERVER: a public address
 *    that is not the caller and is not even stable across one session. Every
 *    sign-in in a cell would name a POP, tagged as a believed client address.
 *    Trusting CloudFront's published ranges instead would also work, but it
 *    means carrying a copy of an AWS-managed prefix list of ~100 entries that
 *    changes without us, and a stale copy silently reintroduces the bug. So the
 *    api is told the header CloudFront writes the VIEWER into — the one part of
 *    the request the viewer cannot forge, because CloudFront strips
 *    client-supplied `CloudFront-*` headers and writes its own — and prefers it
 *    over the chain.
 *
 *    THE HEADER HAS TO SURVIVE TWO HOPS THIS REPOSITORY DOES NOT OWN, and today
 *    it survives neither. In the UI app's Edge stack the behaviour's
 *    `originRequestPolicy` must be ALL_VIEWER_AND_CLOUDFRONT_2022 (it is plain
 *    ALL_VIEWER, which forwards viewer headers but none of CloudFront's own);
 *    and the UI app's api adapter forwards a fixed ALLOWLIST of headers, which
 *    this one is not on. Until both land, a cell records
 *    `client_ip_source = 'proxy'` — our own web tier, marked as NOT the caller —
 *    with one warning per task in the log. That is deliberate: the api does not
 *    paper over the absence with the chain, because the chain's answer in this
 *    topology is an edge server, and a wrong address that reads as a caller is
 *    worse in an append-only evidence store than an honest "we could not tell".
 */
export function apiTrustedProxyEnvironment(cfg: EnvConfig, vpc: ec2.IVpc): Record<string, string> {
  const inFrontOfTheApi = vpc
    .selectSubnets({ subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS })
    .subnets.map((subnet) => subnet.ipv4CidrBlock);
  return {
    RATE_LIMIT_TRUSTED_PROXIES: inFrontOfTheApi.join(','),
    RATE_LIMIT_EDGE_VIEWER_HEADER: CLOUDFRONT_VIEWER_ADDRESS_HEADER,
  };
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
  /** The cell's own SES sending channel (ADR-0027 decision 8). */
  public readonly mailConfigurationSet: ses.ConfigurationSet;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);
    const { cfg } = props;
    this.cluster = props.cluster;

    // ---- mail: the cell's sending channel (ADR-0027) -------------------------
    // The identity itself is account-level and admin-deployed (ZenTax-Dns): one
    // verified domain for both cells, because an SES identity is per account +
    // region and a domain identity covers its subdomains. What IS per cell is
    // this configuration set — so staging's bounces, complaints and delivery
    // metrics are measured apart from production's, and either can be stopped
    // without the other. TLS is required rather than opportunistic: this
    // product carries filing data, and a recipient whose server offers no TLS
    // gets a visible failure instead of a plaintext hop.
    this.mailConfigurationSet = new ses.ConfigurationSet(this, 'MailConfigurationSet', {
      configurationSetName: mailConfigurationSetName(cfg.name),
      tlsPolicy: ses.ConfigurationSetTlsPolicy.REQUIRE,
      reputationMetrics: true,
      sendingEnabled: true,
      // Bounces and complaints suppress the address for this channel only:
      // reputation is per account, so continuing to mail a dead address is how
      // one cell gets the other throttled.
      suppressionReasons: ses.SuppressionReasons.BOUNCES_AND_COMPLAINTS,
    });

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
    // The WORM audit archive (ADR-0008): ONE verb. The api may add an object
    // and do nothing else to this bucket — no delete, no version verb, no
    // `s3:PutObjectRetention` or `s3:PutObjectLegalHold` (the bucket's default
    // retention applies itself at write time, so the writer never has to name
    // it), no `s3:AbortMultipartUpload` (a failed upload is swept by the
    // bucket's lifecycle rule instead, which is the same cleanup without a verb
    // that ends in "Abort"), and no read: the exporter resumes from the sequence
    // number in its own database, not from the archive. One verb is genuinely
    // enough — the S3 adapter issues a single `PutObject` per segment
    // (platform/storage/s3), and `s3:PutObject` would still cover the whole
    // multipart path (create, upload part, complete) if a segment ever grew past
    // the point where the SDK switches.
    //
    // The narrowness is belt to Object Lock's braces: COMPLIANCE mode would
    // refuse a delete even if this statement carried one. Two independent
    // mechanisms, so a change to either alone cannot make the archive mutable.
    apiTaskRole.addToPolicy(new iam.PolicyStatement({
      sid: 'AuditExportPut',
      actions: ['s3:PutObject'],
      resources: [props.auditExportBucket.arnForObjects('*')],
    }));
    props.dataKey.grantEncryptDecrypt(apiTaskRole);
    // Mail: the task role IS the SES credential, so this statement is the whole
    // of what the api may send — and it is narrow in three directions at once.
    // The identity ARN is FORMATTED, not imported: ZenTax-Dns is admin-deployed
    // and deliberately outside the pipeline's dependency graph (ADR-0025), and
    // a domain identity's ARN is fully determined by partition, region, account
    // and name. Both resources are required, not alternatives: a send that
    // names a configuration set is authorized against the identity AND the set.
    // The condition pins the From header to the one address the container is
    // configured with (cfg.mailFromAddress feeds both), so the permission and
    // the configuration cannot drift apart and a bug cannot make the product
    // send as anyone else in the domain. SendRawEmail rides along because in
    // SESv2 it is the same call with MIME content — what a message with an
    // attachment needs.
    apiTaskRole.addToPolicy(new iam.PolicyStatement({
      sid: 'MailSend',
      actions: ['ses:SendEmail', 'ses:SendRawEmail'],
      resources: [
        this.formatArn({ service: 'ses', resource: 'identity', resourceName: cfg.mailIdentityDomain }),
        this.formatArn({ service: 'ses', resource: 'configuration-set', resourceName: this.mailConfigurationSet.configurationSetName }),
      ],
      conditions: { StringEquals: { 'ses:FromAddress': cfg.mailFromAddress } },
    }));

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
        ...apiAuditExportEnvironment(cfg, props.auditExportBucket),
        ...apiMailEnvironment(cfg),
        ...apiPublicEnvironment(cfg),
        ...apiTrustedProxyEnvironment(cfg, props.vpc),
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
