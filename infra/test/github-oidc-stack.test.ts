import { Match } from 'aws-cdk-lib/assertions';
import * as cdk from 'aws-cdk-lib';
import {
  CFN_EXECUTION_POLICY_EDGE_EXPORT_NAME, CFN_EXECUTION_POLICY_EDGE_NAME, CFN_EXECUTION_POLICY_NAME, DEPLOY_REPOSITORIES,
} from '../lib/github-oidc-stack';
import { addGithubOidc } from '../lib/zentax-app';
import { OIDC_EXPORT_NAMES } from './contract';
import {
  cdkJsonContext, ENVS, exportNamesOf, otherEnv, resourcesOfType, synthDns, synthEnv, synthOidc, TestEnv, TITLE_OF,
} from './helpers';

const ROLES = [
  ['staging-eu', 'api'], ['staging-eu', 'web'], ['production-eu', 'api'], ['production-eu', 'web'],
] as const;

const cap = (s: string) => `${s[0].toUpperCase()}${s.slice(1)}`;
const actionsOf = (s: any): string[] => (Array.isArray(s.Action) ? s.Action : [s.Action]);
const resourcesOf = (s: any): string[] => (Array.isArray(s.Resource) ? s.Resource : [s.Resource]);

describe('ZenTax-GithubOidc', () => {
  const template = synthOidc();

  test('creates the GitHub OIDC provider for token.actions.githubusercontent.com', () => {
    template.resourceCountIs('AWS::IAM::OIDCProvider', 1);
    template.hasResourceProperties('AWS::IAM::OIDCProvider', {
      Url: 'https://token.actions.githubusercontent.com',
      ClientIdList: ['sts.amazonaws.com'],
    });
  });

  test('is deployed with the CLI credentials (no bootstrap roles, no BootstrapVersion parameter)', () => {
    expect(template.toJSON().Parameters?.BootstrapVersion).toBeUndefined();
    expect(template.toJSON().Rules?.CheckBootstrapVersion).toBeUndefined();
  });

  test('the two repositories are pinned constants: zentax-api -> api roles, zentax-ui -> web roles; the old githubRepositories context is refused', () => {
    expect(DEPLOY_REPOSITORIES).toEqual({ api: 'mohdhallal/zentax-api', web: 'mohdhallal/zentax-ui' });
    template.resourceCountIs('AWS::IAM::Role', 4);
    expect(cdkJsonContext().githubRepositories).toBeUndefined();
    const app = new cdk.App({ context: { ...cdkJsonContext(), githubRepositories: ['mohdhallal/zentax-api'] } });
    expect(() => addGithubOidc(app)).toThrow(/githubRepositories/);
  });

  test('the cells come from cdk.json: two roles per declared cell, named zentax-deploy-<cell>-{api,web}; a legacy "staging" key fails synth', () => {
    const names = resourcesOfType(template, 'AWS::IAM::Role').map(([, r]) => r.Properties.RoleName).sort();
    expect(names).toEqual([
      'zentax-deploy-production-eu-api', 'zentax-deploy-production-eu-web',
      'zentax-deploy-staging-eu-api', 'zentax-deploy-staging-eu-web',
    ]);
    const context = cdkJsonContext();
    const environments = context.environments as Record<string, unknown>;
    // A third cell (only its key matters to this stack) adds two roles.
    const withUs = new cdk.App({ context: { ...context, environments: { ...environments, 'staging-us': {} } } });
    const t = cdk.assertions.Template.fromStack(addGithubOidc(withUs));
    t.resourceCountIs('AWS::IAM::Role', 6);
    t.hasResourceProperties('AWS::IAM::Role', { RoleName: 'zentax-deploy-staging-us-api' });
    t.hasResourceProperties('AWS::IAM::Role', { RoleName: 'zentax-deploy-staging-us-web' });
    t.hasOutput('DeployRoleArnStagingUsApi', { Export: { Name: 'zentax-deploy-role-staging-us-api' } });
    // The old tier-only names are not cells any more.
    const legacy = new cdk.App({ context: { ...context, environments: { ...environments, staging: {} } } });
    expect(() => addGithubOidc(legacy)).toThrow(/environments\.staging: cell names are/);
  });

  test.each(ROLES)('zentax-deploy-%s-%s trusts only the matching GitHub environment of its one repository', (env, service) => {
    template.hasResourceProperties('AWS::IAM::Role', {
      RoleName: `zentax-deploy-${env}-${service}`,
      AssumeRolePolicyDocument: {
        Statement: [
          {
            Action: 'sts:AssumeRoleWithWebIdentity',
            Effect: 'Allow',
            Principal: { Federated: Match.anyValue() },
            Condition: {
              StringEquals: {
                'token.actions.githubusercontent.com:aud': 'sts.amazonaws.com',
                'token.actions.githubusercontent.com:sub': `repo:${DEPLOY_REPOSITORIES[service]}:environment:${env}`,
              },
            },
          },
        ],
      },
    });
    // The subject never uses a wildcard (no ref:* / * that would admit any branch), and never the other repository / cell.
    const [, role] = resourcesOfType(template, 'AWS::IAM::Role').find(
      ([, r]) => r.Properties.RoleName === `zentax-deploy-${env}-${service}`,
    )!;
    const trust = JSON.stringify(role.Properties.AssumeRolePolicyDocument);
    expect(trust).not.toContain('*');
    expect(trust).not.toContain(DEPLOY_REPOSITORIES[service === 'api' ? 'web' : 'api']);
    expect(trust).not.toContain(`environment:${otherEnv(env)}`);
    // Never the bare tier either (the GitHub environments are named like the cells).
    expect(trust).not.toMatch(/environment:(staging|production)"/);
    expect(role.Properties.AssumeRolePolicyDocument.Statement[0].Condition.StringLike).toBeUndefined();
  });

  test.each(ROLES)('zentax-deploy-%s-%s permissions are scoped to its cell AND its half of the cell', (env, service) => {
    const policies = resourcesOfType(template, 'AWS::IAM::Policy');
    const policy = policies.find(([id]) => id.toLowerCase().startsWith(`deployrole${TITLE_OF[env].toLowerCase()}${service}`));
    expect(policy).toBeDefined();
    const statements = policy![1].Properties.PolicyDocument.Statement as any[];
    const text = JSON.stringify(statements);
    const other = otherEnv(env);
    expect(text).not.toContain(`zentax/${other}`);
    expect(text).not.toContain(`zentax-${other}`);
    expect(text).toContain('cdk-hnb659fds-deploy-role-');

    // Resources render as Fn::Join with the partition/account tokens: match on the rendered text.
    const rendered = (s: any) => JSON.stringify(s.Resource);
    const count = (haystack: string, needle: string) => haystack.split(needle).length - 1;
    const ecr = rendered(statements.find((s) => s.Sid === 'EcrPushPull'));
    const update = rendered(statements.find((s) => s.Sid === 'EcsUpdateService'));
    expect(update).toContain(`service/zentax-${env}/zentax-${env}-${service}`);
    expect(count(update, 'service/zentax-')).toBe(1);
    const passRole = statements.find((s) => s.Sid === 'PassTaskRoles');
    const passRoles = rendered(passRole);
    const logs = rendered(statements.find((s) => s.Sid === 'LogsRead'));
    if (service === 'api') {
      expect(ecr).toContain(`repository/zentax/${env}/api`);
      expect(ecr).toContain(`repository/zentax/${env}/migrate`);
      expect(count(ecr, 'repository/zentax/')).toBe(2);
      for (const c of ['api', 'migrate', 'seed']) expect(passRoles).toContain(`role/zentax-${env}-${c}-*`);
      expect(count(passRoles, ':role/')).toBe(3);
      const run = statements.find((s) => s.Sid === 'EcsRunOneOffTasks');
      expect(run.Action).toBe('ecs:RunTask');
      expect(rendered(run)).toContain(`task-definition/zentax-${env}-migrate:*`);
      expect(rendered(run)).toContain(`task-definition/zentax-${env}-seed:*`);
      expect(count(rendered(run), 'task-definition/')).toBe(2);
      expect(JSON.stringify(run.Condition)).toContain(`cluster/zentax-${env}`);
      expect(text).toContain('ecs:RegisterTaskDefinition');
      expect(logs).toMatch(new RegExp(`/zentax/${env}/(api|migrate|seed)`));
      expect(logs).not.toContain(`/zentax/${env}/web`);
      expect(text).not.toContain(`zentax/${env}/web`);
      expect(text).not.toContain(`zentax-${env}-web`);
    } else {
      expect(ecr).toContain(`repository/zentax/${env}/web`);
      expect(count(ecr, 'repository/zentax/')).toBe(1);
      expect(passRoles).toContain(`role/zentax-${env}-web-*`);
      expect(count(passRoles, ':role/')).toBe(1);
      // The web pipeline never runs one-off tasks or registers task definitions by hand.
      expect(text).not.toContain('ecs:RunTask');
      expect(text).not.toContain('ecs:RegisterTaskDefinition');
      expect(text).not.toContain('ecs:StopTask');
      expect(logs).toContain(`/zentax/${env}/web`);
      for (const c of ['api', 'migrate', 'seed']) {
        expect(text).not.toContain(`zentax/${env}/${c}`);
        expect(text).not.toContain(`zentax-${env}-${c}`);
      }
    }

    // Only actions with no resource-level scope (or read-only ones IAM requires on "*") may use Resource: "*".
    const allowedWildcard = new Set([
      'ecr:GetAuthorizationToken',
      'ecs:RegisterTaskDefinition',
      'ecs:DescribeTaskDefinition',
      'ecs:ListTaskDefinitions',
      'ecs:DescribeClusters',
      'logs:DescribeLogGroups',
      'cloudformation:DescribeStacks',
      'cloudformation:ListStacks',
    ]);
    for (const s of statements) {
      const resources = Array.isArray(s.Resource) ? s.Resource : [s.Resource];
      if (resources.includes('*')) {
        const actions = Array.isArray(s.Action) ? s.Action : [s.Action];
        for (const a of actions) expect(allowedWildcard.has(a)).toBe(true);
      }
    }
    // The CloudFormation reads are read-only and on "*" (the CLI calls them without a StackName).
    const cfn = statements.find((s) => s.Sid === 'CdkReadStacks');
    expect(cfn.Action.sort()).toEqual(['cloudformation:DescribeStacks', 'cloudformation:ListStacks']);
    expect(cfn.Resource).toBe('*');
    expect(text).not.toMatch(/cloudformation:(Create|Update|Delete|Execute)/);
    // iam:PassRole is limited to ECS.
    expect(passRole.Condition).toEqual({ StringEquals: { 'iam:PassedToService': 'ecs-tasks.amazonaws.com' } });
    // Both roles take the CDK deploy path through the (shared) bootstrap roles — documented boundary.
    const assume = statements.find((s) => s.Sid === 'AssumeCdkBootstrapRoles');
    expect(assume.Action).toEqual(['sts:AssumeRole', 'sts:TagSession']);
    for (const r of assume.Resource) expect(JSON.stringify(r)).toMatch(/:role\/cdk-hnb659fds-(deploy|file-publishing|image-publishing|lookup)-role-/);
    expect(assume.Resource).toHaveLength(4);
    // The deploy roles never get the execution policies or AdministratorAccess themselves.
    expect(JSON.stringify(resourcesOfType(template, 'AWS::IAM::Role'))).not.toContain('ManagedPolicyArns');
  });

  test('exports the two execution-policy ARNs and the four deploy-role ARNs under the contract names', () => {
    const names = exportNamesOf(template);
    expect(names.sort()).toEqual([...OIDC_EXPORT_NAMES].sort());
    template.hasOutput('CfnExecutionPolicyArn', { Export: { Name: 'zentax-cfn-execution-policy-arn' } });
    template.hasOutput('CfnExecutionPolicyEdgeArn', { Export: { Name: CFN_EXECUTION_POLICY_EDGE_EXPORT_NAME } });
    expect(CFN_EXECUTION_POLICY_EDGE_EXPORT_NAME).toBe('zentax-cfn-execution-policy-edge-arn');
    for (const [env, service] of ROLES) {
      const id = `DeployRoleArn${TITLE_OF[env]}${cap(service)}`;
      template.hasOutput(id, {
        Export: { Name: `zentax-deploy-role-${env}-${service}` },
        Value: { 'Fn::GetAtt': [Match.stringLikeRegexp(`^DeployRole${TITLE_OF[env]}${cap(service)}`), 'Arn'] },
      });
    }
  });

  describe(`${CFN_EXECUTION_POLICY_NAME} (bootstrap CloudFormation execution policy)`, () => {
    const [, policy] = resourcesOfType(template, 'AWS::IAM::ManagedPolicy').find(
      ([, r]) => r.Properties.ManagedPolicyName === CFN_EXECUTION_POLICY_NAME,
    )!;
    const statements = policy.Properties.PolicyDocument.Statement as any[];
    const text = JSON.stringify(statements);

    test('exists, is exported, and is not AdministratorAccess (no "*" action, no iam:*)', () => {
      template.resourceCountIs('AWS::IAM::ManagedPolicy', 2);
      template.hasOutput('CfnExecutionPolicyArn', { Export: { Name: 'zentax-cfn-execution-policy-arn' } });
      expect(JSON.stringify(template.toJSON())).not.toMatch(/policy\/AdministratorAccess/);
      for (const s of statements) {
        // The single Deny (deploy + bootstrap roles) is asserted separately below.
        if (s.Effect === 'Deny') continue;
        expect(s.Effect).toBe('Allow');
        for (const a of actionsOf(s)) {
          expect(a).not.toBe('*');
          expect(a).not.toBe('iam:*');
          expect(a).not.toMatch(/^(sts|organizations|account|iam:\*)/);
        }
      }
    });

    test('fits the 6144 non-whitespace character limit of a managed policy', () => {
      const rendered = JSON.stringify(policy.Properties.PolicyDocument).replace(/\s/g, '');
      expect(rendered.length).toBeLessThanOrEqual(6144);
    });

    test('still covers what the UI app\'s Web/Edge stacks create (ELB, CloudFront, S3, secrets, Route 53 records) — one execution role for both apps', () => {
      expect(text).toContain('cloudfront:*');
      expect(text).toContain('elasticloadbalancing:*');
      expect(text).toContain(':secret:zentax/*');
      // The Edge stack's alias records + the ACM DNS-validation records go into the ZenTax-Dns zone.
      const route53 = statements.find((s) => actionsOf(s).includes('route53:ChangeResourceRecordSets'))!;
      expect(actionsOf(route53).sort()).toEqual(['route53:ChangeResourceRecordSets', 'route53:GetHostedZone', 'route53:ListResourceRecordSets']);
      for (const r of resourcesOf(route53)) expect(JSON.stringify(r)).toMatch(/:route53:::hostedzone\/\*"/);
      expect(text).toContain('route53:GetChange');
      // This policy is at the size cap, so everything added since it got there
      // lives in the second one, which the bootstrap always takes alongside it:
      // ACM, and hosted-zone create/delete for the Cloud Map private DNS
      // namespace. Route 53 is split by kind — records here, zones there.
      expect(text).not.toContain('acm:');
      expect(text).not.toContain('route53:CreateHostedZone');
      expect(text).not.toContain('route53:DeleteHostedZone');
      // The Web stack replicates zentax/<cell>/origin-verify to us-east-1 (the
      // Edge stack's CloudFront origin resolves it by name there). The grant is
      // secretsmanager:* on the zentax/* secrets in EVERY region, so
      // ReplicateSecretToRegions / RemoveRegionsFromReplication need nothing extra.
      const secrets = statements.filter((s) => actionsOf(s).some((a) => a.startsWith('secretsmanager:')));
      expect(secrets).toHaveLength(1);
      expect(actionsOf(secrets[0])).toEqual(['secretsmanager:*']);
      const secretArns = resourcesOf(secrets[0]).map((r) => JSON.stringify(r));
      expect(secretArns).toHaveLength(1);
      expect(secretArns[0]).toMatch(/:secretsmanager:\*:160117555326:secret:zentax\/\*"/);
    });

    test('IAM role management and PassRole are restricted to zentax-* / ZenTax-* / cdk-* role names', () => {
      const allow = statements.filter((s) => s.Effect === 'Allow');
      const iamStatements = allow.filter((s) => actionsOf(s).some((a) => a.startsWith('iam:')));
      expect(iamStatements.length).toBeGreaterThan(0);
      for (const s of iamStatements) {
        const actions = actionsOf(s);
        const resources = resourcesOf(s).map((r) => JSON.stringify(r));
        expect(resources).not.toContain('"*"');
        if (actions.includes('iam:CreateServiceLinkedRole')) {
          for (const r of resources) expect(r).toContain('role/aws-service-role/');
          continue;
        }
        for (const r of resources) expect(r).toMatch(/:role\/(zentax-\*|ZenTax-\*|cdk-\*)"/);
        for (const a of actions) expect(a).toMatch(/^iam:(\w*Role\w*)$/);
      }
      const passRole = iamStatements.find((s) => actionsOf(s).includes('iam:PassRole'))!;
      expect(passRole.Condition.StringEquals['iam:PassedToService']).toEqual(
        expect.arrayContaining(['ecs-tasks.amazonaws.com', 'lambda.amazonaws.com', 'monitoring.rds.amazonaws.com']),
      );
      // It cannot touch managed policies (including itself), users, groups or the OIDC provider.
      for (const a of allow.flatMap(actionsOf)) {
        expect(a).not.toMatch(/^iam:\w*(?<!Role)Polic(y|ies)(Version|Versions)?$/);
        expect(a).not.toMatch(/^iam:\*/);
      }
      expect(text).not.toMatch(/iam:\w*(User|Group|OpenIDConnectProvider|AccessKey)/);
    });

    test('data-plane resources are name-scoped; "*" only where the API has no resource scope or names are generated', () => {
      const scoped = ['s3:', 'ecr:', 'secretsmanager:', 'logs:*', 'sns:', 'cloudwatch:', 'lambda:', 'ssm:', 'elasticloadbalancing:*', 'rds:*'];
      for (const s of statements) {
        const resources = resourcesOf(s);
        if (!resources.includes('*')) continue;
        for (const a of actionsOf(s)) {
          for (const prefix of scoped) expect(a.startsWith(prefix) && !/(Describe|List|Get)\w*\*?$/.test(a)).toBe(false);
        }
      }
      expect(text).toContain(':secret:zentax/*');
      expect(text).toContain(':repository/zentax/*');
      expect(text).toContain(':::zentax-*');
      expect(text).toContain(':log-group:/zentax/*');
      expect(text).toContain(':log-group:RDSOSMetrics*');
      expect(text).toContain(':alarm:zentax-*');
      // EC2 is limited to the network plane.
      const ec2 = statements.find((s) => actionsOf(s).some((a) => a.startsWith('ec2:')))!;
      expect(actionsOf(ec2)).not.toContain('ec2:*');
      expect(JSON.stringify(actionsOf(ec2))).not.toMatch(/RunInstances|Volume|Image|Snapshot/);
    });

    test('the cell-name change keeps every ZenTax glob valid: zentax-staging-eu-* / zentax/staging-eu/* fall under zentax-* / zentax/*', () => {
      // A glob that had been hard-wired to the old tier-only names would silently stop matching.
      expect(text).not.toMatch(/zentax-(staging|production)\b/);
      expect(text).not.toMatch(/zentax\/(staging|production)\b/);
    });
  });

  describe(`${CFN_EXECUTION_POLICY_EDGE_NAME} (second execution policy: what the first one has no room for)`, () => {
    const [, policy] = resourcesOfType(template, 'AWS::IAM::ManagedPolicy').find(
      ([, r]) => r.Properties.ManagedPolicyName === CFN_EXECUTION_POLICY_EDGE_NAME,
    )!;
    const statements = policy.Properties.PolicyDocument.Statement as any[];
    const sidOf = (name: string) => statements.find((s) => s.Sid === name)!;

    test('grants exactly the six ACM certificate actions on "*" (certificate ARNs are generated), nothing else', () => {
      // Three statements, no more: ACM plus the two Cloud Map zone ones below.
      expect(statements.map((s) => s.Sid)).toEqual([
        'AcmCertificates', 'CloudMapPrivateDnsNamespace', 'CloudMapPrivateDnsNamespaceDelete',
      ]);
      const acm = sidOf('AcmCertificates');
      expect(acm.Effect).toBe('Allow');
      expect(acm.Resource).toBe('*');
      expect(actionsOf(acm).sort()).toEqual([
        'acm:AddTagsToCertificate', 'acm:DeleteCertificate', 'acm:DescribeCertificate',
        'acm:ListTagsForCertificate', 'acm:RemoveTagsFromCertificate', 'acm:RequestCertificate',
      ]);
      const text = JSON.stringify(policy);
      expect(text).not.toMatch(/acm:(Import|Export|Renew|Resend|Update|Put|\*)/);
      expect(text).not.toContain('iam:');
    });

    /**
     * This replaces the older assertion that hosted-zone creation was absent
     * from both policies. The rule it encoded — ADR-0025 decision 8, "the
     * pipeline may issue certificates but never create zones" — was aimed at
     * public zones, but route53:CreateHostedZone takes no resource and has no
     * condition keys, so the same denial also blocked the *private* zone that
     * every cell's Cloud Map namespace is, which would have failed the very
     * first Cluster deploy with AccessDenied.
     *
     * The rule now: zone creation is granted, and it is granted here rather
     * than in the first policy only because that one is at the 6144-character
     * cap — both reach the same bootstrap execution role, so the split is a
     * budget, not a boundary. What keeps the public DNS safe is not IAM: the
     * public zone (ZenTax-Dns, the one Squarespace delegates to) is
     * admin-deployed, never created or replaced by a pipeline, and the zone
     * actions here are only the three Cloud Map needs.
     */
    test('carries hosted-zone create/delete because a Cloud Map private DNS namespace IS a Route 53 private zone', () => {
      // The need: every cell's Cluster stack creates exactly one namespace,
      // and it is load-bearing (api.zentax-<cell>.local is the web tier's
      // GO_API_URL — the API tier has no load balancer).
      for (const env of ENVS) synthEnv(env).cluster.resourceCountIs('AWS::ServiceDiscovery::PrivateDnsNamespace', 1);

      // servicediscovery:CreatePrivateDnsNamespace calls these with the
      // execution role's credentials; neither action takes a resource.
      const create = sidOf('CloudMapPrivateDnsNamespace');
      expect(create.Effect).toBe('Allow');
      expect(actionsOf(create).sort()).toEqual(['route53:CreateHostedZone', 'route53:ListHostedZonesByName']);
      expect(create.Resource).toBe('*');
      // Deletion does take one (rollback of a failed create, and cdk destroy);
      // zone ids are generated, so hostedzone/* is as narrow as it gets.
      const del = sidOf('CloudMapPrivateDnsNamespaceDelete');
      expect(actionsOf(del)).toEqual(['route53:DeleteHostedZone']);
      for (const r of resourcesOf(del)) expect(JSON.stringify(r)).toMatch(/:route53:::hostedzone\/\*"/);

      // Exactly those three zone actions, nothing else from the Route 53 or
      // registrar surface: no delegation-set, DNSSEC, health-check, traffic
      // policy or domain-registration power, and no route53:*.
      const route53 = statements.flatMap(actionsOf).filter((a: string) => a.startsWith('route53:'));
      expect(route53.sort()).toEqual([
        'route53:CreateHostedZone', 'route53:DeleteHostedZone', 'route53:ListHostedZonesByName',
      ]);
      const text = JSON.stringify(policy);
      expect(text).not.toContain('route53:*');
      expect(text).not.toMatch(/route53:\w*(DelegationSet|KeySigningKey|HealthCheck|TrafficPolicy|VPCAssociationAuthorization)/);
      expect(text).not.toContain('route53domains:');
      // Record writes stay in the first policy (asserted there, on hostedzone/*).
      expect(text).not.toContain('route53:ChangeResourceRecordSets');
    });

    test('the compensating control holds: the public zone (ZenTax-Dns) is admin-deployed, not pipeline-deployed', () => {
      // No BootstrapVersion parameter / rule == CliCredentialsStackSynthesizer,
      // i.e. ZenTax-Dns never runs through the execution role these policies
      // scope, so nothing the pipeline is now allowed to create can replace it.
      const dns = synthDns().template.toJSON();
      expect(dns.Parameters?.BootstrapVersion).toBeUndefined();
      expect(dns.Rules?.CheckBootstrapVersion).toBeUndefined();
    });

    test('fits the 6144 non-whitespace character limit and is exported for the bootstrap command', () => {
      const rendered = JSON.stringify(policy.Properties.PolicyDocument).replace(/\s/g, '');
      expect(rendered.length).toBeLessThanOrEqual(6144);
      template.hasOutput('CfnExecutionPolicyEdgeArn', {
        Export: { Name: 'zentax-cfn-execution-policy-edge-arn' },
        Value: { Ref: Match.stringLikeRegexp('^CfnExecutionPolicyEdge') },
      });
    });
  });
});

// The execution policy's `zentax-*` role glob also matches the deploy roles;
// an explicit Deny keeps a pipeline-deployed template from rewriting the
// deploy roles' or the bootstrap roles' policies or trust.
describe('ZenTaxCfnExecutionPolicy denies mutating the deploy and bootstrap roles', () => {
  const template = synthOidc();
  test('carries a Deny iam:* on role/zentax-deploy-* and role/cdk-* (which covers zentax-deploy-staging-eu-api etc.)', () => {
    const [, policy] = resourcesOfType(template, 'AWS::IAM::ManagedPolicy').find(
      ([, r]) => r.Properties.ManagedPolicyName === CFN_EXECUTION_POLICY_NAME,
    )!;
    const statements = policy.Properties.PolicyDocument.Statement as Array<Record<string, unknown>>;
    const deny = statements.find((s) => s.Effect === 'Deny');
    expect(deny).toBeDefined();
    const text = JSON.stringify(deny);
    expect(text).toContain('iam:*');
    expect(text).toContain(':role/zentax-deploy-*');
    expect(text).toContain(':role/cdk-*');
    for (const [env, service] of ROLES as ReadonlyArray<readonly [TestEnv, string]>) {
      expect(`zentax-deploy-${env}-${service}`.startsWith('zentax-deploy-')).toBe(true);
    }
  });
});
