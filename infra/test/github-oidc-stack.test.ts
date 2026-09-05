import { Match } from 'aws-cdk-lib/assertions';
import { CFN_EXECUTION_POLICY_NAME, DEPLOY_REPOSITORIES } from '../lib/github-oidc-stack';
import { OIDC_EXPORT_NAMES } from './contract';
import * as cdk from 'aws-cdk-lib';
import { addGithubOidc } from '../lib/zentax-app';
import { cdkJsonContext, exportNamesOf, resourcesOfType, synthOidc } from './helpers';

const ROLES = [
  ['staging', 'api'], ['staging', 'web'], ['production', 'api'], ['production', 'web'],
] as const;

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
    // The subject never uses a wildcard (no ref:* / * that would admit any branch), and never the other repository.
    const [, role] = resourcesOfType(template, 'AWS::IAM::Role').find(
      ([, r]) => r.Properties.RoleName === `zentax-deploy-${env}-${service}`,
    )!;
    const trust = JSON.stringify(role.Properties.AssumeRolePolicyDocument);
    expect(trust).not.toContain('*');
    expect(trust).not.toContain(DEPLOY_REPOSITORIES[service === 'api' ? 'web' : 'api']);
    expect(trust).not.toContain(env === 'staging' ? 'environment:production' : 'environment:staging');
    expect(role.Properties.AssumeRolePolicyDocument.Statement[0].Condition.StringLike).toBeUndefined();
  });

  test.each(ROLES)('zentax-deploy-%s-%s permissions are scoped to its environment AND its half of the cell', (env, service) => {
    const policies = resourcesOfType(template, 'AWS::IAM::Policy');
    const policy = policies.find(([id]) => id.toLowerCase().startsWith(`deployrole${env}${service}`));
    expect(policy).toBeDefined();
    const statements = policy![1].Properties.PolicyDocument.Statement as any[];
    const text = JSON.stringify(statements);
    const other = env === 'staging' ? 'production' : 'staging';
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
    // The deploy roles never get the execution policy or AdministratorAccess themselves.
    expect(JSON.stringify(resourcesOfType(template, 'AWS::IAM::Role'))).not.toContain('ManagedPolicyArns');
  });

  test('exports the execution-policy ARN and the four deploy-role ARNs under the contract names', () => {
    const names = exportNamesOf(template);
    expect(names.sort()).toEqual([...OIDC_EXPORT_NAMES].sort());
    template.hasOutput('CfnExecutionPolicyArn', { Export: { Name: 'zentax-cfn-execution-policy-arn' } });
    for (const [env, service] of ROLES) {
      const id = `DeployRoleArn${env[0].toUpperCase()}${env.slice(1)}${service[0].toUpperCase()}${service.slice(1)}`;
      template.hasOutput(id, {
        Export: { Name: `zentax-deploy-role-${env}-${service}` },
        Value: { 'Fn::GetAtt': [Match.stringLikeRegexp(`^DeployRole${env[0].toUpperCase()}${env.slice(1)}${service[0].toUpperCase()}${service.slice(1)}`), 'Arn'] },
      });
    }
  });

  describe(`${CFN_EXECUTION_POLICY_NAME} (bootstrap CloudFormation execution policy)`, () => {
    const [, policy] = resourcesOfType(template, 'AWS::IAM::ManagedPolicy').find(
      ([, r]) => r.Properties.ManagedPolicyName === CFN_EXECUTION_POLICY_NAME,
    )!;
    const statements = policy.Properties.PolicyDocument.Statement as any[];
    const text = JSON.stringify(statements);
    const actionsOf = (s: any): string[] => (Array.isArray(s.Action) ? s.Action : [s.Action]);
    const resourcesOf = (s: any): string[] => (Array.isArray(s.Resource) ? s.Resource : [s.Resource]);

    test('exists, is exported, and is not AdministratorAccess (no "*" action, no iam:*)', () => {
      template.resourceCountIs('AWS::IAM::ManagedPolicy', 1);
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

    test('still covers what the UI app\'s Web/Edge stacks create (ELB, CloudFront, S3, secrets, Route 53) — one execution policy for both apps', () => {
      expect(text).toContain('cloudfront:*');
      expect(text).toContain('elasticloadbalancing:*');
      expect(text).toContain(':secret:zentax/*');
      expect(text).toContain('route53:ChangeResourceRecordSets');
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
  });
});

// The execution policy's `zentax-*` role glob also matches the deploy roles;
// an explicit Deny keeps a pipeline-deployed template from rewriting the
// deploy roles' or the bootstrap roles' policies or trust.
describe('ZenTaxCfnExecutionPolicy denies mutating the deploy and bootstrap roles', () => {
  const template = synthOidc();
  test('carries a Deny iam:* on role/zentax-deploy-* and role/cdk-*', () => {
    const [, policy] = resourcesOfType(template, 'AWS::IAM::ManagedPolicy')[0];
    const statements = policy.Properties.PolicyDocument.Statement as Array<Record<string, unknown>>;
    const deny = statements.find((s) => s.Effect === 'Deny');
    expect(deny).toBeDefined();
    const text = JSON.stringify(deny);
    expect(text).toContain('iam:*');
    expect(text).toContain(':role/zentax-deploy-*');
    expect(text).toContain(':role/cdk-*');
  });
});
