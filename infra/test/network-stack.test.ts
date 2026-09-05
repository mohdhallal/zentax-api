import { Match } from 'aws-cdk-lib/assertions';
import { NETWORK_EXPORTS, exportName } from '../lib/exports';
import { ENVS, exportNamesOf, resourcesOfType, synthEnv } from './helpers';

const retention = { staging: 30, production: 365 } as const;

describe.each(ENVS)('ZenTax-%s-Network', (env) => {
  const { network } = synthEnv(env);

  test('flow-log and prefix-list-lookup log groups are explicit, with the environment retention and removal policy', () => {
    network.hasResource('AWS::Logs::LogGroup', {
      Properties: { LogGroupName: `/zentax/${env}/vpc-flow-logs`, RetentionInDays: retention[env] },
      DeletionPolicy: env === 'production' ? 'Retain' : 'Delete',
    });
    network.hasResource('AWS::Logs::LogGroup', {
      Properties: { LogGroupName: `/zentax/${env}/cdk/cloudfront-prefix-list-lookup`, RetentionInDays: retention[env] },
      DeletionPolicy: env === 'production' ? 'Retain' : 'Delete',
    });
    network.allResourcesProperties('AWS::Logs::LogGroup', { RetentionInDays: retention[env] });
    // The flow log writes to that group, and the lookup Lambda logs to the other.
    const [, flowLog] = resourcesOfType(network, 'AWS::EC2::FlowLog')[0];
    expect(JSON.stringify(flowLog.Properties.LogGroupName)).toContain('FlowLogGroup');
    const fn = resourcesOfType(network, 'AWS::Lambda::Function').find(([id]) => id.startsWith('AWS679f53fac002430cb0da5b7982bd2287'))!;
    expect(JSON.stringify(fn[1].Properties.LoggingConfig.LogGroup)).toContain('CloudFrontPrefixListLogGroup');
  });

  test('VPC spans two AZs with public + private-with-egress subnets and the configured NAT count', () => {
    network.resourceCountIs('AWS::EC2::VPC', 1);
    network.resourceCountIs('AWS::EC2::Subnet', 4);
    network.resourceCountIs('AWS::EC2::NatGateway', env === 'production' ? 2 : 1);
  });

  test('gateway endpoint for S3 and interface endpoints for ECR (api + dkr), Secrets Manager and Logs', () => {
    const endpoints = resourcesOfType(network, 'AWS::EC2::VPCEndpoint').map(([, r]) => r.Properties);
    const gateway = endpoints.filter((p) => p.VpcEndpointType === 'Gateway');
    const iface = endpoints.filter((p) => p.VpcEndpointType === 'Interface');
    expect(gateway).toHaveLength(1);
    expect(JSON.stringify(gateway[0].ServiceName)).toContain('.s3');
    const names = iface.map((p) => JSON.stringify(p.ServiceName));
    expect(names.some((n) => n.includes('.ecr.api'))).toBe(true);
    expect(names.some((n) => n.includes('.ecr.dkr'))).toBe(true);
    expect(names.some((n) => n.includes('.secretsmanager'))).toBe(true);
    expect(names.some((n) => n.includes('.logs'))).toBe(true);
  });

  test('no security group admits 0.0.0.0/0 or ::/0 on any port', () => {
    const groups = resourcesOfType(network, 'AWS::EC2::SecurityGroup');
    for (const [, g] of groups) {
      for (const rule of g.Properties.SecurityGroupIngress ?? []) {
        expect(rule.CidrIp).not.toBe('0.0.0.0/0');
        expect(rule.CidrIpv6).not.toBe('::/0');
      }
    }
    for (const [, r] of resourcesOfType(network, 'AWS::EC2::SecurityGroupIngress')) {
      expect(r.Properties.CidrIp).not.toBe('0.0.0.0/0');
      expect(r.Properties.CidrIpv6).not.toBe('::/0');
    }
    expect(groups.length).toBeGreaterThanOrEqual(5);
  });

  test('the ALB security group only admits the CloudFront origin-facing prefix list on port 80', () => {
    const [albId, alb] = resourcesOfType(network, 'AWS::EC2::SecurityGroup').find(([, g]) =>
      g.Properties.GroupName === `zentax-${env}-alb`,
    )!;
    expect(alb.Properties.SecurityGroupIngress ?? []).toHaveLength(0);
    const rules = resourcesOfType(network, 'AWS::EC2::SecurityGroupIngress').filter(
      ([, r]) => JSON.stringify(r.Properties.GroupId).includes(albId),
    );
    expect(rules).toHaveLength(1);
    expect(rules[0][1].Properties).toEqual(
      expect.objectContaining({ FromPort: 80, ToPort: 80, IpProtocol: 'tcp', SourcePrefixListId: expect.anything() }),
    );
    expect(rules[0][1].Properties.CidrIp).toBeUndefined();
    expect(rules[0][1].Properties.SourceSecurityGroupId).toBeUndefined();
    network.hasResourceProperties('Custom::CloudFrontOriginFacingPrefixList', {
      Create: Match.stringLikeRegexp('com.amazonaws.global.cloudfront.origin-facing'),
    });
  });

  test('tiered ingress: web <- ALB, api <- web, db <- api + jobs only', () => {
    const groups = Object.fromEntries(
      resourcesOfType(network, 'AWS::EC2::SecurityGroup').map(([id, g]) => [g.Properties.GroupName, id]),
    ) as Record<string, string>;
    const rules = resourcesOfType(network, 'AWS::EC2::SecurityGroupIngress').map(([, r]) => r.Properties);
    const sources = (target: string) =>
      rules
        .filter((r) => JSON.stringify(r.GroupId).includes(groups[target]))
        .map((r) => ({ from: JSON.stringify(r.SourceSecurityGroupId ?? r.SourcePrefixListId), port: r.FromPort }));

    const web = sources(`zentax-${env}-web`);
    expect(web).toHaveLength(1);
    expect(web[0].from).toContain(groups[`zentax-${env}-alb`]);
    expect(web[0].port).toBe(5000);

    const api = sources(`zentax-${env}-api`);
    expect(api).toHaveLength(1);
    expect(api[0].from).toContain(groups[`zentax-${env}-web`]);
    expect(api[0].port).toBe(3000);

    const db = sources(`zentax-${env}-db`);
    expect(db).toHaveLength(2);
    expect(db.map((d) => d.port)).toEqual([5432, 5432]);
    expect(db.some((d) => d.from.includes(groups[`zentax-${env}-api`]))).toBe(true);
    expect(db.some((d) => d.from.includes(groups[`zentax-${env}-jobs`]))).toBe(true);

    expect(sources(`zentax-${env}-jobs`)).toHaveLength(0);
  });

  test('exports the whole Network part of the contract (VPC, CIDR, AZs, subnets, every security group the UI app needs)', () => {
    const names = exportNamesOf(network);
    for (const key of NETWORK_EXPORTS) expect(names).toContain(exportName(env, key));
    network.hasOutput('VpcId', { Export: { Name: `zentax-${env}-vpc-id` }, Value: { Ref: Match.stringLikeRegexp('^Vpc') } });
    network.hasOutput('VpcCidr', { Export: { Name: `zentax-${env}-vpc-cidr` } });
    network.hasOutput('AvailabilityZones', { Export: { Name: `zentax-${env}-availability-zones` }, Value: 'eu-central-1a,eu-central-1b' });
    // Subnet lists are a comma join of exactly the two subnets of each tier.
    for (const [id, tier] of [['PublicSubnetIds', 'public'], ['PrivateSubnetIds', 'private']] as const) {
      const out = network.toJSON().Outputs[id];
      expect(out.Export.Name).toBe(`zentax-${env}-${tier}-subnet-ids`);
      const [sep, refs] = out.Value['Fn::Join'];
      expect(sep).toBe(',');
      expect(refs).toHaveLength(2);
      for (const ref of refs) expect(JSON.stringify(ref)).toMatch(new RegExp(`Vpc${tier}Subnet`));
    }
    const groups = Object.fromEntries(
      resourcesOfType(network, 'AWS::EC2::SecurityGroup').map(([id, g]) => [g.Properties.GroupName, id]),
    ) as Record<string, string>;
    for (const [id, sg] of [['AlbSecurityGroupId', 'alb'], ['WebSecurityGroupId', 'web'], ['ApiSecurityGroupId', 'api'], ['JobsSecurityGroupId', 'jobs']] as const) {
      network.hasOutput(id, {
        Export: { Name: `zentax-${env}-${sg}-sg-id` },
        Value: { 'Fn::GetAtt': [groups[`zentax-${env}-${sg}`], 'GroupId'] },
      });
    }
  });
});
