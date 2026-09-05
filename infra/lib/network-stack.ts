import * as cdk from 'aws-cdk-lib';
import * as ec2 from 'aws-cdk-lib/aws-ec2';
import * as logs from 'aws-cdk-lib/aws-logs';
import * as cr from 'aws-cdk-lib/custom-resources';
import { Construct } from 'constructs';
import { EnvConfig } from './config';
import { exportName } from './exports';

export interface NetworkStackProps extends cdk.StackProps {
  readonly cfg: EnvConfig;
}

export const API_PORT = 3000;
export const WEB_PORT = 5000;
export const DB_PORT = 5432;

/**
 * ZenTax-<Env>-Network: the VPC, endpoints and EVERY security group of the cell.
 *
 * All security groups (and the rules between them) live here so the Data, Api
 * and (in the UI app) Web stacks only reference them — that keeps the
 * dependency graph a straight line Network -> Data -> Cluster -> Api / Web
 * with no cycles (RDS's group must admit the api tasks' group, and RDS is
 * created before the api service; the web group must admit the ALB's group,
 * which is created by another app).
 *
 * The UI app's Web stack reaches the VPC, the subnets and the alb/web groups
 * only through the exports at the bottom (lib/exports.ts).
 */
export class NetworkStack extends cdk.Stack {
  public readonly vpc: ec2.Vpc;
  /** Public ALB (Web stack, UI app): inbound only from the CloudFront origin-facing managed prefix list. */
  public readonly albSecurityGroup: ec2.SecurityGroup;
  /** web (Express + SPA) tasks (Web stack, UI app): inbound only from the ALB. */
  public readonly webSecurityGroup: ec2.SecurityGroup;
  /** api (Go) tasks: inbound only from web. */
  public readonly apiSecurityGroup: ec2.SecurityGroup;
  /** One-off migrate + seed tasks: no inbound at all. */
  public readonly jobsSecurityGroup: ec2.SecurityGroup;
  /** RDS: inbound only from api + jobs. */
  public readonly dbSecurityGroup: ec2.SecurityGroup;

  private readonly configuredAvailabilityZones: string[];

  /**
   * The AZs come from configuration, never from an `availability-zones:...`
   * context lookup: synth must not call AWS (the only credentials on the dev
   * machine are the root user's), and the Vpc construct validates its explicit
   * AZs against this getter.
   */
  public get availabilityZones(): string[] {
    return this.configuredAvailabilityZones ?? super.availabilityZones;
  }

  constructor(scope: Construct, id: string, props: NetworkStackProps) {
    super(scope, id, props);
    const { cfg } = props;
    this.configuredAvailabilityZones = cfg.availabilityZones;

    this.vpc = new ec2.Vpc(this, 'Vpc', {
      vpcName: `zentax-${cfg.name}`,
      ipAddresses: ec2.IpAddresses.cidr(cfg.cidr),
      availabilityZones: cfg.availabilityZones,
      natGateways: cfg.natGateways,
      subnetConfiguration: [
        { name: 'public', subnetType: ec2.SubnetType.PUBLIC, cidrMask: 24 },
        { name: 'private', subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS, cidrMask: 22 },
      ],
      restrictDefaultSecurityGroup: true,
    });
    // The built-in CloudFormation validator reports W3010 ("avoid hard-coding
    // AZs") on the subnets; that is the deliberate trade-off described above.

    // VPC flow logs to CloudWatch (rejects only: cheap, and what security monitoring wants).
    // Explicit log group: the environment's retention and removal policy, not
    // the construct's 2-year default.
    const flowLogGroup = new logs.LogGroup(this, 'FlowLogGroup', {
      logGroupName: `/zentax/${cfg.name}/vpc-flow-logs`,
      retention: cfg.logRetentionDays,
      removalPolicy: cfg.dataRemovalPolicy,
    });
    this.vpc.addFlowLog('RejectFlowLog', {
      trafficType: ec2.FlowLogTrafficType.REJECT,
      destination: ec2.FlowLogDestination.toCloudWatchLogs(flowLogGroup),
    });

    // Endpoints so image pulls, secret reads and log writes never traverse NAT
    // (cheaper, and no dependency on the internet path for the control plane).
    this.vpc.addGatewayEndpoint('S3', { service: ec2.GatewayVpcEndpointAwsService.S3 });
    const privateOnly = { subnets: { subnetType: ec2.SubnetType.PRIVATE_WITH_EGRESS } };
    const endpointSg = new ec2.SecurityGroup(this, 'EndpointSg', {
      vpc: this.vpc,
      description: 'ZenTax interface endpoints: HTTPS from inside the VPC only',
      allowAllOutbound: false,
    });
    endpointSg.addIngressRule(ec2.Peer.ipv4(cfg.cidr), ec2.Port.tcp(443), 'HTTPS from the VPC');
    const interfaceEndpoints: Record<string, ec2.InterfaceVpcEndpointAwsService> = {
      EcrApi: ec2.InterfaceVpcEndpointAwsService.ECR,
      EcrDocker: ec2.InterfaceVpcEndpointAwsService.ECR_DOCKER,
      SecretsManager: ec2.InterfaceVpcEndpointAwsService.SECRETS_MANAGER,
      CloudWatchLogs: ec2.InterfaceVpcEndpointAwsService.CLOUDWATCH_LOGS,
    };
    for (const [name, service] of Object.entries(interfaceEndpoints)) {
      this.vpc.addInterfaceEndpoint(name, {
        service,
        ...privateOnly,
        securityGroups: [endpointSg],
        privateDnsEnabled: true,
        open: false, // endpointSg carries the single VPC-CIDR:443 rule; `open` would duplicate it
      });
    }

    // ---- Security groups ----------------------------------------------------
    this.albSecurityGroup = new ec2.SecurityGroup(this, 'AlbSg', {
      vpc: this.vpc,
      securityGroupName: `zentax-${cfg.name}-alb`,
      description: 'ZenTax public ALB: HTTP only from the CloudFront origin-facing prefix list',
      allowAllOutbound: true,
    });
    this.webSecurityGroup = new ec2.SecurityGroup(this, 'WebSg', {
      vpc: this.vpc,
      securityGroupName: `zentax-${cfg.name}-web`,
      description: 'ZenTax web tasks: only the ALB may connect',
      allowAllOutbound: true,
    });
    this.apiSecurityGroup = new ec2.SecurityGroup(this, 'ApiSg', {
      vpc: this.vpc,
      securityGroupName: `zentax-${cfg.name}-api`,
      description: 'ZenTax api tasks: only web tasks may connect',
      allowAllOutbound: true,
    });
    this.jobsSecurityGroup = new ec2.SecurityGroup(this, 'JobsSg', {
      vpc: this.vpc,
      securityGroupName: `zentax-${cfg.name}-jobs`,
      description: 'ZenTax one-off migrate/seed tasks: no inbound',
      allowAllOutbound: true,
    });
    this.dbSecurityGroup = new ec2.SecurityGroup(this, 'DbSg', {
      vpc: this.vpc,
      securityGroupName: `zentax-${cfg.name}-db`,
      description: 'ZenTax RDS: Postgres only from api and one-off job tasks',
      allowAllOutbound: false,
    });

    const cloudFrontPrefixListId = cfg.cloudFrontPrefixListId ?? this.lookupCloudFrontPrefixList(cfg);
    this.albSecurityGroup.addIngressRule(
      ec2.Peer.prefixList(cloudFrontPrefixListId),
      ec2.Port.tcp(80),
      'HTTP from CloudFront (origin-facing managed prefix list)',
    );
    this.webSecurityGroup.addIngressRule(this.albSecurityGroup, ec2.Port.tcp(WEB_PORT), 'web from ALB');
    this.apiSecurityGroup.addIngressRule(this.webSecurityGroup, ec2.Port.tcp(API_PORT), 'api from web');
    this.dbSecurityGroup.addIngressRule(this.apiSecurityGroup, ec2.Port.tcp(DB_PORT), 'Postgres from api');
    this.dbSecurityGroup.addIngressRule(this.jobsSecurityGroup, ec2.Port.tcp(DB_PORT), 'Postgres from migrate/seed');

    // ---- Outputs: the export contract (UI app's Web stack + CI `ecs run-task`) ----
    const out = (id: string, key: Parameters<typeof exportName>[1], value: string, description?: string) =>
      new cdk.CfnOutput(this, id, { value, description, exportName: exportName(cfg.name, key) });
    const subnetIds = (subnetType: ec2.SubnetType) => cdk.Fn.join(',', this.vpc.selectSubnets({ subnetType }).subnetIds);
    out('VpcId', 'vpc-id', this.vpc.vpcId);
    out('VpcCidr', 'vpc-cidr', this.vpc.vpcCidrBlock);
    out('AvailabilityZones', 'availability-zones', cfg.availabilityZones.join(','), 'Comma-separated AZs, in subnet order');
    out('PublicSubnetIds', 'public-subnet-ids', subnetIds(ec2.SubnetType.PUBLIC), 'Comma-separated public subnet IDs (the ALB)');
    out('PrivateSubnetIds', 'private-subnet-ids', subnetIds(ec2.SubnetType.PRIVATE_WITH_EGRESS),
      'Comma-separated private subnet IDs (services, ecs run-task network configuration)');
    out('AlbSecurityGroupId', 'alb-sg-id', this.albSecurityGroup.securityGroupId, 'Security group of the public ALB (Web stack)');
    out('WebSecurityGroupId', 'web-sg-id', this.webSecurityGroup.securityGroupId, 'Security group of the web tasks (Web stack)');
    out('ApiSecurityGroupId', 'api-sg-id', this.apiSecurityGroup.securityGroupId, 'Security group of the api tasks');
    out('JobsSecurityGroupId', 'jobs-sg-id', this.jobsSecurityGroup.securityGroupId,
      'Security group for the one-off migrate/seed tasks (ecs run-task)');
  }

  /**
   * Resolve the ID of `com.amazonaws.global.cloudfront.origin-facing` at deploy
   * time (the ID is region-specific and there is no CFN intrinsic for it).
   * A context lookup would need AWS credentials at synth; this does not.
   */
  private lookupCloudFrontPrefixList(cfg: EnvConfig): string {
    // The provider Lambda's log group, with the environment's retention/removal
    // (instead of the construct-managed 2-year default).
    const lookupLogGroup = new logs.LogGroup(this, 'CloudFrontPrefixListLogGroup', {
      logGroupName: `/zentax/${cfg.name}/cdk/cloudfront-prefix-list-lookup`,
      retention: cfg.logRetentionDays,
      removalPolicy: cfg.dataRemovalPolicy,
    });
    const lookup = new cr.AwsCustomResource(this, 'CloudFrontPrefixList', {
      resourceType: 'Custom::CloudFrontOriginFacingPrefixList',
      onCreate: {
        service: 'EC2',
        action: 'describeManagedPrefixLists',
        parameters: {
          Filters: [{ Name: 'prefix-list-name', Values: ['com.amazonaws.global.cloudfront.origin-facing'] }],
        },
        physicalResourceId: cr.PhysicalResourceId.of('com.amazonaws.global.cloudfront.origin-facing'),
      },
      onUpdate: {
        service: 'EC2',
        action: 'describeManagedPrefixLists',
        parameters: {
          Filters: [{ Name: 'prefix-list-name', Values: ['com.amazonaws.global.cloudfront.origin-facing'] }],
        },
        physicalResourceId: cr.PhysicalResourceId.of('com.amazonaws.global.cloudfront.origin-facing'),
      },
      policy: cr.AwsCustomResourcePolicy.fromSdkCalls({ resources: cr.AwsCustomResourcePolicy.ANY_RESOURCE }),
      installLatestAwsSdk: false,
      logGroup: lookupLogGroup,
    });
    return lookup.getResponseField('PrefixLists.0.PrefixListId');
  }
}
