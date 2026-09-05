import * as cdk from 'aws-cdk-lib';
import { ApiStack } from './api-stack';
import { EnvConfig, EnvName, envTags, loadDnsConfig, loadEnvConfig, loadGithubOidcConfig } from './config';
import { DataStack } from './data-stack';
import { ClusterStack } from './cluster-stack';
import { DnsStack } from './dns-stack';
import { GithubOidcStack } from './github-oidc-stack';
import { NetworkStack } from './network-stack';

export interface EnvironmentStacks {
  readonly cfg: EnvConfig;
  readonly network: NetworkStack;
  readonly data: DataStack;
  readonly cluster: ClusterStack;
  readonly api: ApiStack;
}

/**
 * Synthesize the four API-owned stacks of one cell
 * (Network -> Data -> Cluster -> Api). The UI app (zentax-ui/infra) adds
 * ZenTax-<Title>-Web and -Edge on top, through the export contract only.
 */
export function addEnvironment(app: cdk.App, name: EnvName): EnvironmentStacks {
  const cfg = loadEnvConfig(app, name);
  const env = { account: cfg.account, region: cfg.region };
  const prefix = `ZenTax-${cfg.title}`;
  const tags = envTags(cfg);

  const network = new NetworkStack(app, `${prefix}-Network`, { env, cfg, tags, description: `ZenTax ${cfg.name}: VPC, endpoints, security groups` });
  const data = new DataStack(app, `${prefix}-Data`, {
    env,
    cfg,
    tags,
    description: `ZenTax ${cfg.name}: KMS, RDS Postgres 16, secrets, documents bucket, api/migrate ECR, log groups, alarm topic`,
    vpc: network.vpc,
    dbSecurityGroup: network.dbSecurityGroup,
  });
  const cluster = new ClusterStack(app, `${prefix}-Cluster`, {
    env,
    cfg,
    tags,
    description: `ZenTax ${cfg.name}: ECS cluster + Cloud Map namespace, one-off migrate + seed task definitions`,
    vpc: network.vpc,
    database: data.database,
    masterSecret: data.masterSecret,
    appDbSecret: data.appDbSecret,
    authEncryptionKeySecret: data.authEncryptionKeySecret,
    seedAdminSecret: data.seedAdminSecret,
    documentsBucket: data.documentsBucket,
    repositories: data.repositories,
    logGroups: data.logGroups,
  });
  const api = new ApiStack(app, `${prefix}-Api`, {
    env,
    cfg,
    tags,
    description: `ZenTax ${cfg.name}: ECS Fargate api service (Cloud Map "api")`,
    vpc: network.vpc,
    apiSecurityGroup: network.apiSecurityGroup,
    cluster: cluster.cluster,
    namespaceName: cluster.namespaceName,
    database: data.database,
    dataKey: data.dataKey,
    appDbSecret: data.appDbSecret,
    authEncryptionKeySecret: data.authEncryptionKeySecret,
    documentsBucket: data.documentsBucket,
    repositories: data.repositories,
    logGroups: data.logGroups,
    alarmTopic: data.alarmTopic,
  });
  // Cross-stack references order deployment Network -> Data -> Cluster -> Api.
  return { cfg, network, data, cluster, api };
}

/**
 * Synthesize the account-level GitHub OIDC stack. It is deployed by the admin
 * identity with the CLI's own credentials (CliCredentialsStackSynthesizer):
 * not through the bootstrap roles, so it can exist before the bootstrap and
 * the scoped execution policy it creates never needs power over itself.
 */
export function addGithubOidc(app: cdk.App): GithubOidcStack {
  const cfg = loadGithubOidcConfig(app);
  return new GithubOidcStack(app, 'ZenTax-GithubOidc', {
    env: { account: cfg.account },
    cfg,
    tags: { Project: 'ZenTax', ManagedBy: 'cdk' },
    description: 'ZenTax: GitHub Actions OIDC provider, per-cell api + web deploy roles, CloudFormation execution policies',
    synthesizer: new cdk.CliCredentialsStackSynthesizer(),
  });
}

/**
 * Synthesize the account-level DNS stack (the apex hosted zone + its
 * records). Admin-deployed with the CLI's own credentials, like the OIDC
 * stack: hosted-zone creation is never part of the pipeline's execution
 * policy. Route 53 is global; the stack lives in the home region. The
 * registrar delegates to this zone, so the stack is termination-protected and
 * the zone + every record set are RETAINed (dns-stack.ts).
 */
export function addDns(app: cdk.App): DnsStack {
  const cfg = loadDnsConfig(app);
  return new DnsStack(app, 'ZenTax-Dns', {
    env: { account: cfg.account, region: cfg.region },
    cfg,
    tags: { Project: 'ZenTax', ManagedBy: 'cdk' },
    description: `ZenTax: Route 53 hosted zone for ${cfg.zoneName} (Squarespace site + Google Workspace mail records, SPF, DMARC)`,
    synthesizer: new cdk.CliCredentialsStackSynthesizer(),
    terminationProtection: true,
  });
}
