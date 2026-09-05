import { Match } from 'aws-cdk-lib/assertions';
import { API_EXPORTS, exportName } from '../lib/exports';
import { ENVS, exportNamesOf, resourcesOfType, synthEnv, synthEnvWith, taskDefinition } from './helpers';

const expected = {
  staging: { desired: 1 },
  production: { desired: 2 },
} as const;

describe.each(ENVS)('ZenTax-%s-Api', (env) => {
  const want = expected[env];
  const { api } = synthEnv(env);

  test('api registers as "api" in the Cluster stack\'s Cloud Map namespace', () => {
    api.resourceCountIs('AWS::ECS::Cluster', 0);
    api.resourceCountIs('AWS::ServiceDiscovery::PrivateDnsNamespace', 0);
    api.hasResourceProperties('AWS::ServiceDiscovery::Service', {
      Name: 'api',
      DnsConfig: Match.objectLike({ DnsRecords: [{ Type: 'A', TTL: 10 }] }),
    });
  });

  test('api task definition: contract env vars present, DB_PASSWORD and AUTH_ENCRYPTION_KEY are secrets only', () => {
    const td = taskDefinition(api, '-api');
    expect(td.Properties.Family).toBe(`zentax-${env}-api`);
    expect(td.Properties.Cpu).toBe('512');
    expect(td.Properties.Memory).toBe('1024');
    const container = td.Properties.ContainerDefinitions[0];
    const envNames = container.Environment.map((e: any) => e.Name);
    expect(envNames).toEqual(
      expect.arrayContaining([
        'APP_ENV', 'DB_HOST', 'DB_PORT', 'DB_NAME', 'DB_USER', 'DB_SSLMODE',
        'CORS_ALLOWED_ORIGINS', 'STORAGE_DRIVER', 'STORAGE_S3_BUCKET', 'STORAGE_S3_REGION', 'LOG_FORMAT',
      ]),
    );
    expect(envNames).not.toContain('DB_PASSWORD');
    expect(envNames).not.toContain('AUTH_ENCRYPTION_KEY');
    const byName = Object.fromEntries(container.Environment.map((e: any) => [e.Name, e.Value]));
    expect(byName.APP_ENV).toBe(env);
    expect(byName.DB_SSLMODE).toBe('require');
    expect(byName.DB_USER).toBe('zentax_app');
    expect(byName.DB_NAME).toBe('zentax');
    expect(byName.STORAGE_DRIVER).toBe('s3');
    expect(byName.LOG_FORMAT).toBe('json');
    expect(JSON.stringify(byName.STORAGE_S3_BUCKET)).toContain('DocumentsBucket');
    expect(JSON.stringify(byName.CORS_ALLOWED_ORIGINS)).toContain('https://');

    const secretNames = container.Secrets.map((s: any) => s.Name);
    expect(secretNames.sort()).toEqual(['AUTH_ENCRYPTION_KEY', 'DB_PASSWORD']);
    for (const s of container.Secrets) {
      expect(s.ValueFrom).toBeDefined();
      expect(s.Value).toBeUndefined();
    }
    expect(container.PortMappings[0].ContainerPort).toBe(3000);
    expect(JSON.stringify(container.HealthCheck.Command)).toContain('/health');
    expect(JSON.stringify(container.Image)).toContain('ApiRepo');
  });

  test('CORS_ALLOWED_ORIGINS is the reserved placeholder by default and the configured public origin(s) when set', () => {
    const byName = (t: import('aws-cdk-lib/assertions').Template) =>
      Object.fromEntries(taskDefinition(t, '-api').Properties.ContainerDefinitions[0].Environment.map((e: any) => [e.Name, e.Value]));
    expect(byName(api).CORS_ALLOWED_ORIGINS).toBe(`https://zentax-${env}.invalid`);
    const set = synthEnvWith(env, { envOverrides: { corsAllowedOrigins: 'https://d1234.cloudfront.net,https://app.example.com' } });
    expect(byName(set.api).CORS_ALLOWED_ORIGINS).toBe('https://d1234.cloudfront.net,https://app.example.com');
    expect(() => synthEnvWith(env, { envOverrides: { corsAllowedOrigins: '*' } })).toThrow(/corsAllowedOrigins/);
    expect(() => synthEnvWith(env, { envOverrides: { corsAllowedOrigins: 'https://a.example/path' } })).toThrow(/corsAllowedOrigins/);
  });

  test('only the api task definition and service live here; deployment circuit breaker with rollback and CPU autoscaling to 2x', () => {
    api.resourceCountIs('AWS::ECS::TaskDefinition', 1);
    api.resourceCountIs('AWS::ECS::Service', 1);
    api.hasResourceProperties('AWS::ECS::Service', {
      ServiceName: `zentax-${env}-api`,
      DeploymentConfiguration: Match.objectLike({ DeploymentCircuitBreaker: { Enable: true, Rollback: true } }),
      DesiredCount: want.desired,
      LaunchType: 'FARGATE',
      NetworkConfiguration: Match.objectLike({ AwsvpcConfiguration: Match.objectLike({ AssignPublicIp: 'DISABLED' }) }),
    });
    // The service joins the Cluster stack's cluster and uses the Network stack's api group.
    const [, service] = resourcesOfType(api, 'AWS::ECS::Service')[0];
    expect(JSON.stringify(service.Properties.Cluster)).toContain('ZenTax-');
    expect(JSON.stringify(service.Properties.NetworkConfiguration.AwsvpcConfiguration.SecurityGroups)).toContain('ApiSg');
    api.resourceCountIs('AWS::ApplicationAutoScaling::ScalableTarget', 1);
    api.hasResourceProperties('AWS::ApplicationAutoScaling::ScalableTarget', {
      MinCapacity: want.desired,
      MaxCapacity: 2 * want.desired,
    });
    api.hasResourceProperties('AWS::ApplicationAutoScaling::ScalingPolicy', {
      TargetTrackingScalingPolicyConfiguration: Match.objectLike({
        TargetValue: 60,
        PredefinedMetricSpecification: { PredefinedMetricType: 'ECSServiceAverageCPUUtilization' },
      }),
    });
  });

  test('has no public surface: no load balancer, listener, target group, CloudFront distribution, bucket or secret of its own', () => {
    for (const type of [
      'AWS::ElasticLoadBalancingV2::LoadBalancer', 'AWS::ElasticLoadBalancingV2::Listener', 'AWS::ElasticLoadBalancingV2::TargetGroup',
      'AWS::CloudFront::Distribution', 'AWS::S3::Bucket', 'AWS::SecretsManager::Secret', 'AWS::ECR::Repository', 'AWS::Logs::LogGroup',
      'AWS::SNS::Topic', 'AWS::EC2::SecurityGroup', 'AWS::EC2::SecurityGroupIngress',
    ]) {
      api.resourceCountIs(type, 0);
    }
    expect(JSON.stringify(api.toJSON())).not.toContain('origin-verify');
  });

  test('alarm: api running-task floor (Container Insights), wired to the Data stack\'s topic', () => {
    const alarms = resourcesOfType(api, 'AWS::CloudWatch::Alarm').map(([, r]) => r.Properties);
    expect(alarms.map((a) => a.AlarmName)).toEqual([`zentax-${env}-api-running-tasks`]);
    const [running] = alarms;
    expect(running.Namespace).toBe('ECS/ContainerInsights');
    expect(running.MetricName).toBe('RunningTaskCount');
    expect(running.Threshold).toBe(want.desired);
    expect(running.ComparisonOperator).toBe('LessThanThreshold');
    expect(running.Period).toBe(60);
    expect(running.EvaluationPeriods).toBe(5);
    expect(running.TreatMissingData).toBe('breaching');
    expect(running.AlarmActions).toHaveLength(1);
    expect(running.OKActions).toHaveLength(1);
    expect(JSON.stringify(running.AlarmActions[0])).toContain('Alarms');
    const dims = Object.fromEntries(running.Dimensions.map((d: any) => [d.Name, d.Value]));
    expect(JSON.stringify(dims.ClusterName)).toContain('Cluster');
    expect(JSON.stringify(dims.ServiceName)).toContain('ApiService');
  });

  test('the container logs to the Data stack\'s /zentax/<env>/api log group', () => {
    const logging = taskDefinition(api, '-api').Properties.ContainerDefinitions[0].LogConfiguration;
    expect(logging.LogDriver).toBe('awslogs');
    expect(JSON.stringify(logging.Options['awslogs-group'])).toContain('ApiLogGroup');
    expect(logging.Options['awslogs-stream-prefix']).toBe('api');
  });

  test('api task role: explicit object verbs + ListBucket on the documents bucket, KMS on the data key, no *Version and no wildcards', () => {
    const policies = resourcesOfType(api, 'AWS::IAM::Policy');
    const apiTaskPolicy = policies.find(([id]) => id.startsWith('apiTaskRole'));
    expect(apiTaskPolicy).toBeDefined();
    const statements = apiTaskPolicy![1].Properties.PolicyDocument.Statement as any[];
    const text = JSON.stringify(statements);
    const actions = statements.flatMap((s) => (Array.isArray(s.Action) ? s.Action : [s.Action])) as string[];
    const s3Actions = actions.filter((a) => a.startsWith('s3:')).sort();
    expect(s3Actions).toEqual(['s3:AbortMultipartUpload', 's3:DeleteObject', 's3:GetObject', 's3:ListBucket', 's3:PutObject']);
    expect(text).not.toContain('s3:DeleteObjectVersion');
    expect(text).not.toContain('s3:*');
    expect(text).not.toMatch(/s3:List\w*Versions/);
    expect(text).not.toMatch(/s3:\w*Version/);
    expect(text).toContain('kms:Decrypt');
    expect(text).toContain('kms:Encrypt');
    // ListBucket is on the bucket ARN itself; the object verbs on <bucket>/*.
    const list = statements.find((s) => s.Sid === 'DocumentsList');
    expect(JSON.stringify(list.Resource)).not.toContain('/*');
    const objects = statements.find((s) => s.Sid === 'DocumentsObjects');
    expect(JSON.stringify(objects.Resource)).toContain('/*');
    for (const s of statements) {
      const resources = Array.isArray(s.Resource) ? s.Resource : [s.Resource];
      for (const r of resources) expect(r).not.toBe('*');
    }
    api.hasResourceProperties('AWS::IAM::Role', { RoleName: `zentax-${env}-api-task` });
    api.hasResourceProperties('AWS::IAM::Role', { RoleName: `zentax-${env}-api-exec` });
  });

  test('exports the Api part of the contract: service name, internal URL for the web tier, pinned task-definition ARN', () => {
    const names = exportNamesOf(api);
    for (const key of API_EXPORTS) expect(names).toContain(exportName(env, key));
    api.hasOutput('ApiServiceName', { Export: { Name: `zentax-${env}-api-service-name` }, Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('^ApiService'), 'Name'] } });
    api.hasOutput('ApiInternalUrl', { Export: { Name: `zentax-${env}-api-internal-url` }, Value: `http://api.zentax-${env}.local:3000` });
    api.hasOutput('ApiTaskDefinitionArn', { Export: { Name: `zentax-${env}-api-task-arn` }, Value: { Ref: Match.stringLikeRegexp('^ApiTask') } });
  });

  test('no literal secret values anywhere in the template', () => {
    const text = JSON.stringify(api.toJSON());
    expect(text).not.toMatch(/"(DB_PASSWORD|AUTH_ENCRYPTION_KEY|PGPASSWORD|APP_DB_PASSWORD)"\s*,\s*"Value"/);
  });
});
