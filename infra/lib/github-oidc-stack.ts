import * as cdk from 'aws-cdk-lib';
import * as iam from 'aws-cdk-lib/aws-iam';
import { Construct } from 'constructs';
import { EnvName, envTitle, GithubOidcConfig } from './config';
import { OIDC_EXPORTS } from './exports';

export interface GithubOidcStackProps extends cdk.StackProps {
  readonly cfg: GithubOidcConfig;
}

export const GITHUB_OIDC_HOST = 'token.actions.githubusercontent.com';
export const CFN_EXECUTION_POLICY_NAME = 'ZenTaxCfnExecutionPolicy';
/**
 * The second execution policy: what only the UI app's Edge stack needs (ACM,
 * for the custom-domain certificate). It is a separate managed policy because
 * the first one sits at the 6144-character cap; both go to
 * `cdk bootstrap --cloudformation-execution-policies`, comma-separated.
 */
export const CFN_EXECUTION_POLICY_EDGE_NAME = 'ZenTaxCfnExecutionPolicyEdge';
/**
 * Deliberately NOT in lib/exports.ts (which must stay byte-identical in both
 * repositories): no stack imports this value — only the bootstrap command in
 * the README reads it.
 */
export const CFN_EXECUTION_POLICY_EDGE_EXPORT_NAME = 'zentax-cfn-execution-policy-edge-arn';

/** The two halves of a cell, each deployed by its own repository. */
export type DeployService = 'api' | 'web';
export const DEPLOY_SERVICES: readonly DeployService[] = ['api', 'web'];

/**
 * The repository each deploy role trusts. One role per repository AND
 * environment: the API repository's role can only touch the api half of a
 * cell, the UI repository's only the web half (see makeDeployRole).
 */
export const DEPLOY_REPOSITORIES: Record<DeployService, string> = {
  api: 'mohdhallal/zentax-api',
  web: 'mohdhallal/zentax-ui',
};

/**
 * ZenTax-GithubOidc: account-level, region-agnostic. The GitHub Actions OIDC
 * provider plus two deploy roles per cell (ADR-0016: no long-lived keys in
 * CI) — `zentax-deploy-<cell>-api` for the API repository and
 * `zentax-deploy-<cell>-web` for the UI repository (`staging-eu`,
 * `production-eu`: the cells cdk.json declares). A role only trusts jobs
 * running in the matching GitHub *environment* (named like the cell) of its
 * one repository, so production-eu's protection rules gate its roles.
 *
 * It also owns `ZenTaxCfnExecutionPolicy` (+ `ZenTaxCfnExecutionPolicyEdge`),
 * the policies the CDK bootstrap's CloudFormation execution role gets instead
 * of AdministratorAccess. This stack is deployed by the admin identity with
 * its own credentials (the app gives it the CliCredentialsStackSynthesizer,
 * so it never runs through the scoped execution role and the policies never
 * have to grant power over themselves).
 */
export class GithubOidcStack extends cdk.Stack {
  public readonly provider: iam.OidcProviderNative;
  public readonly roles: Record<EnvName, Record<DeployService, iam.Role>>;
  public readonly cfnExecutionPolicy: iam.ManagedPolicy;
  public readonly cfnExecutionPolicyEdge: iam.ManagedPolicy;

  constructor(scope: Construct, id: string, props: GithubOidcStackProps) {
    super(scope, id, props);
    const ENVIRONMENTS = props.cfg.environments;

    this.cfnExecutionPolicy = this.makeCfnExecutionPolicy();
    new cdk.CfnOutput(this, 'CfnExecutionPolicyArn', {
      value: this.cfnExecutionPolicy.managedPolicyArn,
      exportName: OIDC_EXPORTS.cfnExecutionPolicyArn,
      description: 'Pass to `cdk bootstrap --cloudformation-execution-policies` (replaces AdministratorAccess), together with CfnExecutionPolicyEdgeArn',
    });
    this.cfnExecutionPolicyEdge = this.makeCfnExecutionPolicyEdge();
    new cdk.CfnOutput(this, 'CfnExecutionPolicyEdgeArn', {
      value: this.cfnExecutionPolicyEdge.managedPolicyArn,
      exportName: CFN_EXECUTION_POLICY_EDGE_EXPORT_NAME,
      description: 'Second policy for `cdk bootstrap --cloudformation-execution-policies` (comma-separated with CfnExecutionPolicyArn): ACM for the Edge stack',
    });

    this.provider = new iam.OidcProviderNative(this, 'GithubProvider', {
      url: `https://${GITHUB_OIDC_HOST}`,
      clientIds: ['sts.amazonaws.com'],
      // GitHub's root CA thumbprints; AWS now validates against its trusted CA
      // store for this issuer, but the property is still required.
      thumbprints: ['6938fd4d98bab03faadb97b34396831e3780aea1', '1c58a3a8518e8759bf075b76b750d4f2df264fcd'],
    });

    const roles: Partial<Record<EnvName, Record<DeployService, iam.Role>>> = {};
    for (const env of ENVIRONMENTS) {
      roles[env] = {
        api: this.makeDeployRole(env, 'api'),
        web: this.makeDeployRole(env, 'web'),
      };
    }
    this.roles = roles as Record<EnvName, Record<DeployService, iam.Role>>;

    new cdk.CfnOutput(this, 'ProviderArn', { value: this.provider.oidcProviderArn });
    for (const env of ENVIRONMENTS) {
      for (const service of DEPLOY_SERVICES) {
        new cdk.CfnOutput(this, `DeployRoleArn${envTitle(env)}${capitalize(service)}`, {
          value: this.roles[env][service].roleArn,
          exportName: OIDC_EXPORTS.deployRoleArn(env, service),
          description: `role-to-assume for the GitHub "${env}" environment of ${DEPLOY_REPOSITORIES[service]}`,
        });
      }
    }
  }

  /**
   * One deploy role = one repository x one cell x one half of the cell.
   *
   *   api: push zentax/<env>/{api,migrate}; run the migrate + seed tasks (and
   *        register a revision of them with the new image tag); roll the api
   *        service; read the api/migrate/seed logs.
   *   web: push zentax/<env>/web; roll the web service; read the web logs.
   *
   * Both take the CDK deploy path (assume the bootstrap roles): the
   * CloudFormation execution role is shared per account, so the *template* is
   * where the two halves meet — that is the documented shared boundary (README:
   * "Why staging-eu and production-eu share the deploy boundary today"; the same
   * holds between the api and web halves until each has its own account).
   */
  private makeDeployRole(env: EnvName, service: DeployService): iam.Role {
    const repository = DEPLOY_REPOSITORIES[service];
    // Names derive from the cell name: role zentax-deploy-staging-eu-api,
    // cluster zentax-staging-eu, images zentax/staging-eu/*, task roles
    // zentax-staging-eu-<component>-*, logs /zentax/staging-eu/<component>.
    const role = new iam.Role(this, `DeployRole${envTitle(env)}${capitalize(service)}`, {
      roleName: `zentax-deploy-${env}-${service}`,
      description: `GitHub Actions deploy role for ZenTax ${env} ${service} (${repository}, OIDC, no static keys)`,
      maxSessionDuration: cdk.Duration.hours(1),
      assumedBy: new iam.WebIdentityPrincipal(this.provider.oidcProviderArn, {
        StringEquals: {
          [`${GITHUB_OIDC_HOST}:aud`]: 'sts.amazonaws.com',
          [`${GITHUB_OIDC_HOST}:sub`]: `repo:${repository}:environment:${env}`,
        },
      }),
    });

    const partition = this.partition;
    const account = this.account;
    const clusterArn = `arn:${partition}:ecs:*:${account}:cluster/zentax-${env}`;
    // What this half of the cell owns: its images, its ECS components, its
    // task/execution roles and its log groups.
    const components = service === 'api' ? ['api', 'migrate', 'seed'] : ['web'];
    const repoArns = components
      .filter((c) => c !== 'seed') // the seed task runs the api image
      .map((c) => `arn:${partition}:ecr:*:${account}:repository/zentax/${env}/${c}`);
    const roleArns = components.map((c) => `arn:${partition}:iam::${account}:role/zentax-${env}-${c}-*`);
    const logGroupArns = components.flatMap((c) => [
      `arn:${partition}:logs:*:${account}:log-group:/zentax/${env}/${c}`,
      `arn:${partition}:logs:*:${account}:log-group:/zentax/${env}/${c}:log-stream:*`,
    ]);

    role.addToPolicy(new iam.PolicyStatement({
      sid: 'EcrLogin',
      actions: ['ecr:GetAuthorizationToken'],
      resources: ['*'], // this action has no resource-level scope
    }));
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'EcrPushPull',
      actions: [
        'ecr:BatchCheckLayerAvailability',
        'ecr:BatchGetImage',
        'ecr:GetDownloadUrlForLayer',
        'ecr:InitiateLayerUpload',
        'ecr:UploadLayerPart',
        'ecr:CompleteLayerUpload',
        'ecr:PutImage',
        'ecr:DescribeImages',
        'ecr:DescribeRepositories',
        'ecr:ListImages',
      ],
      resources: repoArns,
    }));

    if (service === 'api') {
      role.addToPolicy(new iam.PolicyStatement({
        sid: 'EcsDescribeAndRegister',
        actions: ['ecs:RegisterTaskDefinition', 'ecs:DescribeTaskDefinition', 'ecs:ListTaskDefinitions', 'ecs:DescribeClusters'],
        resources: ['*'], // RegisterTaskDefinition/DescribeTaskDefinition have no resource-level scope
      }));
      role.addToPolicy(new iam.PolicyStatement({
        sid: 'EcsRunOneOffTasks',
        actions: ['ecs:RunTask'],
        resources: [
          `arn:${partition}:ecs:*:${account}:task-definition/zentax-${env}-migrate:*`,
          `arn:${partition}:ecs:*:${account}:task-definition/zentax-${env}-seed:*`,
        ],
        conditions: { ArnEquals: { 'ecs:cluster': clusterArn } },
      }));
      role.addToPolicy(new iam.PolicyStatement({
        sid: 'EcsTasks',
        actions: ['ecs:DescribeTasks', 'ecs:ListTasks', 'ecs:StopTask'],
        resources: [`arn:${partition}:ecs:*:${account}:task/zentax-${env}/*`],
        conditions: { ArnEquals: { 'ecs:cluster': clusterArn } },
      }));
    }
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'EcsUpdateService',
      actions: ['ecs:UpdateService', 'ecs:DescribeServices'],
      resources: [`arn:${partition}:ecs:*:${account}:service/zentax-${env}/zentax-${env}-${service}`],
    }));
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'PassTaskRoles',
      actions: ['iam:PassRole'],
      resources: roleArns,
      conditions: { StringEquals: { 'iam:PassedToService': 'ecs-tasks.amazonaws.com' } },
    }));

    role.addToPolicy(new iam.PolicyStatement({
      sid: 'LogsRead',
      actions: ['logs:GetLogEvents', 'logs:FilterLogEvents', 'logs:DescribeLogStreams'],
      resources: logGroupArns,
    }));
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'LogsDescribeGroups',
      actions: ['logs:DescribeLogGroups'],
      resources: ['*'], // no resource-level scope
    }));

    // CDK deploy path: the CLI assumes the bootstrap roles; they trust the account.
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'AssumeCdkBootstrapRoles',
      actions: ['sts:AssumeRole', 'sts:TagSession'],
      resources: [
        `arn:${partition}:iam::${account}:role/cdk-hnb659fds-deploy-role-${account}-*`,
        `arn:${partition}:iam::${account}:role/cdk-hnb659fds-file-publishing-role-${account}-*`,
        `arn:${partition}:iam::${account}:role/cdk-hnb659fds-image-publishing-role-${account}-*`,
        `arn:${partition}:iam::${account}:role/cdk-hnb659fds-lookup-role-${account}-*`,
      ],
    }));
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'CdkBootstrapVersionCheck',
      actions: ['ssm:GetParameter', 'ssm:GetParameters'],
      resources: [`arn:${partition}:ssm:*:${account}:parameter/cdk-bootstrap/hnb659fds/version`],
    }));
    role.addToPolicy(new iam.PolicyStatement({
      sid: 'CdkReadStacks',
      // Read-only. Both calls are made without a StackName by the CLI and the
      // workflow scripts, and the IAM reference requires "*" for that form.
      actions: ['cloudformation:DescribeStacks', 'cloudformation:ListStacks'],
      resources: ['*'],
    }));

    return role;
  }

  /**
   * What CloudFormation may do while deploying the ZenTax stacks — the
   * execution-role policy for `cdk bootstrap --cloudformation-execution-policies`.
   *
   * Scoping rules: resource ARNs wherever ZenTax names them (`zentax-*`,
   * `/zentax/*`, `zentax/*`), CloudFormation-generated names via the stack
   * prefix (`ZenTax-*`), the bootstrap's own resources via `cdk-*`; service-wide
   * `Resource: "*"` only where the API has no resource-level scope or the
   * physical name is generated (ECS, Service Discovery, Application Auto
   * Scaling, CloudFront, RDS, KMS). IAM role management is limited to those
   * three name prefixes and `iam:PassRole` to the same, which is also the
   * boundary's known weakness: a template may still create a `zentax-*` role
   * with any policy. Separate accounts per environment close that (README:
   * "Why staging-eu and production-eu share the deploy boundary today").
   *
   * A missing permission surfaces as an AccessDenied on the resource in the
   * CloudFormation events; extend the statement here and redeploy this stack
   * (admin credentials) — the bootstrap keeps pointing at the same policy ARN.
   *
   * Managed policies are capped at 6144 non-whitespace characters, which is
   * why the statements carry no Sids and EC2 uses action patterns (the test
   * suite checks the size) — and why what the Edge stack's custom domain
   * additionally needs lives in a second policy (makeCfnExecutionPolicyEdge).
   */
  private makeCfnExecutionPolicy(): iam.ManagedPolicy {
    const p = this.partition;
    const a = this.account;
    // Role management is limited to the names ZenTax stacks create; the deploy
    // roles (zentax-deploy-*) and the bootstrap roles (cdk-*) are excluded by
    // an explicit Deny below, so a template can never widen its own boundary.
    const roleArns = ['zentax-*', 'ZenTax-*'].map((n) => `arn:${p}:iam::${a}:role/${n}`);
    const s = (actions: string[], resources: string[], conditions?: Record<string, unknown>) =>
      new iam.PolicyStatement({ actions, resources, conditions });

    return new iam.ManagedPolicy(this, 'CfnExecutionPolicy', {
      managedPolicyName: CFN_EXECUTION_POLICY_NAME,
      description: 'ZenTax: CloudFormation execution policy for the CDK bootstrap (what the ZenTax stacks create; replaces AdministratorAccess)',
      statements: [
        // EC2 network plane only (VPC, subnets, routing, gateways, security
        // groups, endpoints, flow logs, tags) — no instances, volumes, AMIs.
        s([
          'ec2:Describe*', 'ec2:GetManagedPrefixListEntries',
          'ec2:*Vpc', 'ec2:ModifyVpcAttribute', 'ec2:*Subnet', 'ec2:ModifySubnetAttribute',
          'ec2:*RouteTable', 'ec2:*Route', 'ec2:*InternetGateway', 'ec2:*NatGateway',
          'ec2:AllocateAddress', 'ec2:ReleaseAddress',
          'ec2:*SecurityGroup', 'ec2:*SecurityGroupIngress', 'ec2:*SecurityGroupEgress',
          'ec2:UpdateSecurityGroupRuleDescriptions*',
          'ec2:*VpcEndpoint', 'ec2:*VpcEndpoints', 'ec2:*FlowLogs', 'ec2:*Tags',
        ], ['*']),
        // Physical names are generated or the API has no resource scope.
        s(['ecs:*', 'servicediscovery:*', 'application-autoscaling:*', 'cloudfront:*'], ['*']),
        // KMS: creating a key has no resource; everything else only on keys the
        // ZenTax stacks tagged (Project=ZenTax travels with CreateKey) and on
        // the zentax/* aliases — never on the bootstrap or any foreign key.
        s(['kms:CreateKey', 'kms:Describe*', 'kms:List*'], ['*']),
        s([
          'kms:*KeyPolicy', 'kms:*KeyRotation*', 'kms:*Resource*', 'kms:UpdateKeyDescription',
          'kms:EnableKey', 'kms:DisableKey', 'kms:*KeyDeletion',
        ], [`arn:${p}:kms:*:${a}:key/*`], { StringEquals: { 'aws:ResourceTag/Project': 'ZenTax' } }),
        s(['kms:*Alias'], [`arn:${p}:kms:*:${a}:alias/zentax/*`, `arn:${p}:kms:*:${a}:key/*`]),
        // Read-only calls that IAM only allows on "*" (+ the stack-output reads
        // behind weak cross-stack references, read-only too).
        s([
          'elasticloadbalancing:Describe*', 'rds:Describe*', 'rds:ListTagsForResource', 'logs:DescribeLogGroups',
          'route53:GetChange', 'route53:ListHostedZones', 'route53:AssociateVPCWithHostedZone',
          'tag:GetResources', 'tag:TagResources', 'tag:UntagResources', 'cloudformation:DescribeStacks',
        ], ['*']),
        // loadbalancer/app/…, listener/app/…, listener-rule/app/… share one pattern.
        s(['elasticloadbalancing:*'], [
          `arn:${p}:elasticloadbalancing:*:${a}:*/app/zentax-*`,
          `arn:${p}:elasticloadbalancing:*:${a}:targetgroup/zentax-*`,
        ]),
        s(['rds:*'], [
          `arn:${p}:rds:*:${a}:db:zentax-*`,
          `arn:${p}:rds:*:${a}:pg:zentax-*`,
          `arn:${p}:rds:*:${a}:subgrp:zentax-*`,
          `arn:${p}:rds:*:${a}:og:default*`,
          `arn:${p}:rds:*:${a}:snapshot:*`,
        ]),
        s(['s3:*'], [`arn:${p}:s3:::zentax-*`, `arn:${p}:s3:::zentax-*/*`]),
        // Custom-resource Lambda code is fetched from the bootstrap assets bucket.
        s(['s3:GetObject*', 's3:GetBucket*', 's3:List*'], [
          `arn:${p}:s3:::cdk-hnb659fds-assets-*`,
          `arn:${p}:s3:::cdk-hnb659fds-assets-*/*`,
        ]),
        s(['ecr:*'], [`arn:${p}:ecr:*:${a}:repository/zentax/*`]),
        s(['secretsmanager:*'], [`arn:${p}:secretsmanager:*:${a}:secret:zentax/*`]),
        s(['logs:*'], [
          `arn:${p}:logs:*:${a}:log-group:/zentax/*`,
          `arn:${p}:logs:*:${a}:log-group:/aws/rds/instance/zentax-*`,
          `arn:${p}:logs:*:${a}:log-group:RDSOSMetrics*`,
          `arn:${p}:logs:*:${a}:log-group:/aws/lambda/ZenTax-*`,
        ]),
        s(['cloudwatch:*'], [`arn:${p}:cloudwatch:*:${a}:alarm:zentax-*`]),
        s(['sns:*'], [`arn:${p}:sns:*:${a}:zentax-*`]),
        s(['lambda:*'], [`arn:${p}:lambda:*:${a}:function:ZenTax-*`, `arn:${p}:lambda:*:${a}:function:zentax-*`]),
        s([
          'ssm:GetParameter', 'ssm:GetParameters', 'ssm:PutParameter', 'ssm:DeleteParameter', 'ssm:DeleteParameters',
          'ssm:AddTagsToResource', 'ssm:RemoveTagsFromResource', 'ssm:ListTagsForResource',
        ], [
          `arn:${p}:ssm:*:${a}:parameter/cdk-bootstrap/*`,
          `arn:${p}:ssm:*:${a}:parameter/cdk/exports/*`,
          `arn:${p}:ssm:*:${a}:parameter/zentax/*`,
        ]),
        s(['route53:ChangeResourceRecordSets', 'route53:GetHostedZone', 'route53:ListResourceRecordSets'], [
          `arn:${p}:route53:::hostedzone/*`,
        ]),
        // IAM: only roles named by ZenTax, by CloudFormation for ZenTax stacks, or by the bootstrap.
        s([
          'iam:CreateRole', 'iam:DeleteRole', 'iam:GetRole', 'iam:UpdateRole', 'iam:UpdateRoleDescription',
          'iam:UpdateAssumeRolePolicy', 'iam:TagRole', 'iam:UntagRole', 'iam:ListRoleTags',
          'iam:PutRolePolicy', 'iam:DeleteRolePolicy', 'iam:GetRolePolicy', 'iam:ListRolePolicies',
          'iam:AttachRolePolicy', 'iam:DetachRolePolicy', 'iam:ListAttachedRolePolicies',
          'iam:PutRolePermissionsBoundary', 'iam:DeleteRolePermissionsBoundary', 'iam:ListInstanceProfilesForRole',
        ], roleArns),
        s(['iam:PassRole'], roleArns, {
          StringEquals: {
            'iam:PassedToService': [
              'ecs-tasks.amazonaws.com', 'lambda.amazonaws.com', 'monitoring.rds.amazonaws.com', 'vpc-flow-logs.amazonaws.com',
            ],
          },
        }),
        s(['iam:CreateServiceLinkedRole', 'iam:GetRole'], [`arn:${p}:iam::${a}:role/aws-service-role/*`]),
        // The `zentax-*` glob above also matches the deploy roles: an explicit
        // Deny keeps any template deployed through a pipeline from rewriting
        // the deploy roles' or the bootstrap roles' policies or trust.
        new iam.PolicyStatement({
          effect: iam.Effect.DENY,
          actions: ['iam:*'],
          resources: [`arn:${p}:iam::${a}:role/zentax-deploy-*`, `arn:${p}:iam::${a}:role/cdk-*`],
        }),
      ],
    });
  }

  /**
   * What CloudFormation additionally needs for the UI app's Edge stack once a
   * custom domain is on: the ACM certificate it creates in-stack (DNS
   * validation into the ZenTax-Dns hosted zone — the Route 53 record calls are
   * already in the first policy, on `hostedzone/*`). Certificate ARNs are
   * generated, so the resource is `*`; the action list is explicit — no
   * import/export/renew. Hosted-zone CREATION is deliberately absent from
   * both policies: ZenTax-Dns is admin-deployed.
   */
  private makeCfnExecutionPolicyEdge(): iam.ManagedPolicy {
    return new iam.ManagedPolicy(this, 'CfnExecutionPolicyEdge', {
      managedPolicyName: CFN_EXECUTION_POLICY_EDGE_NAME,
      description: 'ZenTax: second CloudFormation execution policy for the CDK bootstrap — ACM for the UI app\'s Edge stack (custom-domain certificate)',
      statements: [
        new iam.PolicyStatement({
          sid: 'AcmCertificates',
          actions: [
            'acm:RequestCertificate', 'acm:DescribeCertificate', 'acm:DeleteCertificate',
            'acm:AddTagsToCertificate', 'acm:RemoveTagsFromCertificate', 'acm:ListTagsForCertificate',
          ],
          resources: ['*'],
        }),
      ],
    });
  }
}

function capitalize(s: string): string {
  return `${s[0].toUpperCase()}${s.slice(1)}`;
}
