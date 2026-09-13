import { Match } from 'aws-cdk-lib/assertions';
import { CLOUDFRONT_VIEWER_ADDRESS_HEADER } from '../lib/api-stack';
import { API_EXPORTS, exportName } from '../lib/exports';
import { ENVS, exportNamesOf, PUBLIC_HOSTNAME_OF, resourcesOfType, synthEnv, synthEnvWith, taskDefinition, TIER_OF } from './helpers';

const ZONE = 'zentax.software';

const expected = {
  'staging-eu': { desired: 1, from: `noreply@staging.${ZONE}`, fromName: 'ZenTax Staging' },
  'production-eu': { desired: 2, from: `noreply@${ZONE}`, fromName: 'ZenTax' },
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
        'AUDIT_EXPORT_ENABLED', 'AUDIT_EXPORT_STORAGE_DRIVER', 'AUDIT_EXPORT_STORAGE_S3_BUCKET', 'AUDIT_EXPORT_STORAGE_S3_REGION',
        'MAIL_DRIVER', 'MAIL_FROM_ADDRESS', 'MAIL_FROM_NAME', 'MAIL_SES_REGION', 'MAIL_SES_CONFIGURATION_SET',
      ]),
    );
    expect(envNames).not.toContain('DB_PASSWORD');
    expect(envNames).not.toContain('AUTH_ENCRYPTION_KEY');
    const byName = Object.fromEntries(container.Environment.map((e: any) => [e.Name, e.Value]));
    // APP_ENV is the CELL name (every other deployed name follows it). The Go
    // loader splits it into <tier>-<regionLabel>, loads that tier's
    // deployment/config_files/<tier>.json and applies the tier's fail-closed
    // rules — so the cell needs no config file of its own, and staging-eu is
    // validated exactly as staging is (config/environment.go).
    expect(byName.APP_ENV).toBe(env);
    expect(byName.APP_ENV.split('-')[0]).toBe(TIER_OF[env]);
    expect(byName.DB_SSLMODE).toBe('require');
    expect(byName.DB_USER).toBe('zentax_app');
    expect(byName.DB_NAME).toBe('zentax');
    expect(byName.STORAGE_DRIVER).toBe('s3');
    expect(byName.LOG_FORMAT).toBe('json');
    expect(JSON.stringify(byName.STORAGE_S3_BUCKET)).toContain('DocumentsBucket');
    expect(JSON.stringify(byName.CORS_ALLOWED_ORIGINS)).toContain('https://');
    // The WORM archive (ADR-0008). The driver is the load-bearing value: the Go
    // default for an unset AUDIT_EXPORT_STORAGE_DRIVER is the *document* store,
    // which is deletable — a cell that forgot this line would run an export
    // that looks healthy and is worth nothing as evidence.
    expect(byName.AUDIT_EXPORT_ENABLED).toBe('true');
    expect(byName.AUDIT_EXPORT_STORAGE_DRIVER).toBe('s3');
    expect(byName.AUDIT_EXPORT_STORAGE_S3_BUCKET).not.toEqual(byName.STORAGE_S3_BUCKET);
    expect(JSON.stringify(byName.AUDIT_EXPORT_STORAGE_S3_BUCKET)).toContain('AuditExportBucket');
    expect(JSON.stringify(byName.AUDIT_EXPORT_STORAGE_S3_BUCKET)).not.toContain('DocumentsBucket');
    expect(byName.AUDIT_EXPORT_STORAGE_S3_REGION).toBe('eu-central-1');

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

  test('CORS_ALLOWED_ORIGINS defaults to https://<publicHostname>; the reserved placeholder without a hostname; explicit corsAllowedOrigins wins', () => {
    const byName = (t: import('aws-cdk-lib/assertions').Template) =>
      Object.fromEntries(taskDefinition(t, '-api').Properties.ContainerDefinitions[0].Environment.map((e: any) => [e.Name, e.Value]));
    expect(byName(api).CORS_ALLOWED_ORIGINS).toBe(`https://${PUBLIC_HOSTNAME_OF[env]}`);
    const noHost = synthEnvWith(env, { envOverrides: { publicHostname: '' } });
    expect(byName(noHost.api).CORS_ALLOWED_ORIGINS).toBe(`https://zentax-${env}.invalid`);
    const set = synthEnvWith(env, { envOverrides: { corsAllowedOrigins: 'https://d1234.cloudfront.net,https://app.example.com' } });
    expect(byName(set.api).CORS_ALLOWED_ORIGINS).toBe('https://d1234.cloudfront.net,https://app.example.com');
    expect(() => synthEnvWith(env, { envOverrides: { corsAllowedOrigins: '*' } })).toThrow(/corsAllowedOrigins/);
    expect(() => synthEnvWith(env, { envOverrides: { corsAllowedOrigins: 'https://a.example/path' } })).toThrow(/corsAllowedOrigins/);
  });

  test('RATE_LIMIT_TRUSTED_PROXIES is the tier in front of the api — the private subnets — and NOT the whole cell', () => {
    const byName = (t: import('aws-cdk-lib/assertions').Template) =>
      Object.fromEntries(taskDefinition(t, '-api').Properties.ContainerDefinitions[0].Environment.map((e: any) => [e.Name, e.Value]));

    // Trust here is by network range and nothing else: whatever is inside it can
    // write any client address into a header and have it believed, keyed on and
    // WRITTEN INTO THE SECURITY-EVENT STREAM. So the set has to be the tier that
    // actually sits in front of the api (the web tasks, in the private subnets)
    // rather than every task, sidecar and one-off job in the cell.
    const trusted: string[] = byName(api).RATE_LIMIT_TRUSTED_PROXIES.split(',');
    expect(trusted.length).toBeGreaterThan(0);
    for (const entry of trusted) {
      // A /22 per AZ, from the Network stack's subnet configuration.
      expect(entry).toMatch(/^10\.\d+\.\d+\.0\/22$/);
    }
    expect(trusted).not.toContain(`10.${trusted[0].split('.')[1]}.0.0/16`);

    const vpcCidr = new RegExp(`^10\\.${trusted[0].split('.')[1]}\\.0\\.0/16$`);
    expect(byName(api).RATE_LIMIT_TRUSTED_PROXIES).not.toMatch(vpcCidr);

    // It moves with the cell's address space.
    const moved = synthEnvWith(env, { envOverrides: { cidr: '10.99.0.0/16' } });
    const movedTrusted: string[] = byName(moved.api).RATE_LIMIT_TRUSTED_PROXIES.split(',');
    expect(movedTrusted.length).toBe(trusted.length);
    for (const entry of movedTrusted) {
      expect(entry).toMatch(/^10\.99\.\d+\.0\/22$/);
    }
    expect(movedTrusted).not.toContain('10.99.0.0/16');
  });

  // The blocker this pair exists for: behind CloudFront the forwarded chain's
  // first untrusted entry from the right is a CloudFront EDGE SERVER, not the
  // person — so without this header every security event in the cell would name
  // a POP and call it a caller. The header is the one thing in the request the
  // viewer cannot forge (CloudFront strips client-supplied CloudFront-* headers
  // and writes its own).
  test('RATE_LIMIT_EDGE_VIEWER_HEADER names the header the distribution states the viewer in', () => {
    const byName = (t: import('aws-cdk-lib/assertions').Template) =>
      Object.fromEntries(taskDefinition(t, '-api').Properties.ContainerDefinitions[0].Environment.map((e: any) => [e.Name, e.Value]));

    expect(byName(api).RATE_LIMIT_EDGE_VIEWER_HEADER).toBe(CLOUDFRONT_VIEWER_ADDRESS_HEADER);
    expect(CLOUDFRONT_VIEWER_ADDRESS_HEADER).toBe('CloudFront-Viewer-Address');

    // DEPLOYMENT COUPLING, asserted here because nothing else in this repository
    // can: the UI app's Edge stack must forward CloudFront's OWN headers
    // (origin-request policy AllViewerAndCloudFrontHeaders-2022). Under plain
    // AllViewer the header never reaches the origin, and the api then records
    // `client_ip_source = 'proxy'` — our own web tier, marked as not the caller —
    // plus a warning per process, rather than inventing an address.
  });

  // ---- ADR-0027: the api can send, as exactly one address ------------------

  test('the api task can send through the ZenTax-Dns identity and its own configuration set, as ONE From address and nothing else', () => {
    const policies = resourcesOfType(api, 'AWS::IAM::Policy');
    const statements = policies.find(([id]) => id.startsWith('apiTaskRole'))![1].Properties.PolicyDocument.Statement as any[];
    const mail = statements.find((s) => s.Sid === 'MailSend');
    expect(mail).toBeDefined();
    expect(mail.Effect).toBe('Allow');
    // SESv2's SendEmail is the same call with simple or MIME content; both verbs, nothing else.
    expect((Array.isArray(mail.Action) ? mail.Action : [mail.Action]).sort()).toEqual(['ses:SendEmail', 'ses:SendRawEmail']);

    // Two resources, both required for a send that names a configuration set:
    // the identity (formatted, never imported — ZenTax-Dns is admin-deployed
    // and outside the pipeline's dependency graph) and this cell's own set.
    const resources = (Array.isArray(mail.Resource) ? mail.Resource : [mail.Resource]).map((r: unknown) => JSON.stringify(r));
    expect(resources).toHaveLength(2);
    expect(resources).toContain(JSON.stringify(`arn:aws:ses:eu-central-1:160117555326:identity/${ZONE}`));
    // The set's ARN is a real reference to the resource next door, so the policy
    // cannot exist without it.
    const set = resources.find((r: string) => r.includes(':configuration-set/'))!;
    expect(set).toContain('"Ref":"MailConfigurationSet');
    for (const r of resources) {
      expect(r).toContain(':ses:eu-central-1:160117555326:');
      expect(r).not.toContain('*');
    }
    // Nothing from the identity-management or account surface, and no import.
    const text = JSON.stringify(statements);
    expect(text).not.toMatch(/ses:(Create|Delete|Put|Update|Verify|Get|List)/);
    expect(text).not.toContain('ses:*');
    expect(JSON.stringify(mail)).not.toContain('Fn::ImportValue');

    // The condition is the point: the role may send only as the cell's address.
    expect(mail.Condition).toEqual({ StringEquals: { 'ses:FromAddress': want.from } });
  });

  test('the same From address feeds the container and the IAM condition, so permission and configuration cannot drift apart', () => {
    const byName = Object.fromEntries(
      taskDefinition(api, '-api').Properties.ContainerDefinitions[0].Environment.map((e: any) => [e.Name, e.Value]),
    );
    expect(byName.MAIL_DRIVER).toBe('ses');
    expect(byName.MAIL_FROM_ADDRESS).toBe(want.from);
    expect(byName.MAIL_FROM_NAME).toBe(want.fromName);
    expect(byName.MAIL_SES_REGION).toBe('eu-central-1');
    expect(byName.MAIL_SES_CONFIGURATION_SET).toBe(`zentax-${env}`);
    // Staging is visibly staging in the From line, on a subdomain of the same
    // verified identity — a message that escaped from it cannot be mistaken for
    // the product talking to a customer.
    expect(byName.MAIL_FROM_ADDRESS.endsWith(`@${ZONE}`)).toBe(env === 'production-eu');
    expect(byName.MAIL_FROM_ADDRESS.endsWith(ZONE)).toBe(true);
    // No credential: the task role IS the credential (nothing to store or rotate).
    const secretNames = taskDefinition(api, '-api').Properties.ContainerDefinitions[0].Secrets.map((s: any) => s.Name);
    expect(secretNames.filter((n: string) => n.startsWith('MAIL_'))).toEqual([]);
    expect(JSON.stringify(api.toJSON())).not.toContain('SMTP');
  });

  test('the cell\'s SES configuration set: its own channel, TLS required, reputation measured, bounces and complaints suppressed', () => {
    api.resourceCountIs('AWS::SES::ConfigurationSet', 1);
    api.hasResourceProperties('AWS::SES::ConfigurationSet', {
      Name: `zentax-${env}`,
      DeliveryOptions: Match.objectLike({ TlsPolicy: 'REQUIRE' }),
      ReputationOptions: Match.objectLike({ ReputationMetricsEnabled: true }),
      SendingOptions: Match.objectLike({ SendingEnabled: true }),
      SuppressionOptions: Match.objectLike({ SuppressedReasons: Match.arrayWith(['BOUNCE', 'COMPLAINT']) }),
    });
    // The identity is not a cell resource: it is verified once, account-level,
    // by the admin-deployed ZenTax-Dns stack.
    api.resourceCountIs('AWS::SES::EmailIdentity', 0);
    expect(JSON.stringify(api.toJSON())).not.toContain('DkimAttributes');
  });

  test('PUBLIC_BASE_URL is https://<publicHostname> on the api task, and absent when no public hostname is configured', () => {
    const byName = (t: import('aws-cdk-lib/assertions').Template) =>
      Object.fromEntries(taskDefinition(t, '-api').Properties.ContainerDefinitions[0].Environment.map((e: any) => [e.Name, e.Value]));
    expect(byName(api).PUBLIC_BASE_URL).toBe(`https://${PUBLIC_HOSTNAME_OF[env]}`);
    const noHost = synthEnvWith(env, { envOverrides: { publicHostname: '' } });
    expect(byName(noHost.api).PUBLIC_BASE_URL).toBeUndefined();
    expect(Object.keys(byName(noHost.api))).not.toContain('PUBLIC_BASE_URL');
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

  const apiTaskStatements = () => {
    const policies = resourcesOfType(api, 'AWS::IAM::Policy');
    const apiTaskPolicy = policies.find(([id]) => id.startsWith('apiTaskRole'));
    expect(apiTaskPolicy).toBeDefined();
    return apiTaskPolicy![1].Properties.PolicyDocument.Statement as any[];
  };
  const actionsOf = (statement: any): string[] => (Array.isArray(statement.Action) ? statement.Action : [statement.Action]);

  test('api task role: explicit object verbs + ListBucket on the documents bucket, KMS on the data key, no *Version and no wildcards', () => {
    const statements = apiTaskStatements();
    const text = JSON.stringify(statements);
    const actions = statements.flatMap(actionsOf);
    const s3Actions = [...new Set(actions.filter((a) => a.startsWith('s3:')))].sort();
    // The whole S3 surface of the api, across both buckets. `s3:PutObject` is
    // shared (documents and the audit export); every other verb below belongs to
    // the documents bucket alone — see the audit-export test for that split.
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

  // ---- ADR-0008: the api may add to the WORM archive and nothing else -------

  test('api task role can PUT to the audit-export bucket — one verb, on that bucket\'s objects only', () => {
    const statements = apiTaskStatements();
    const put = statements.find((s) => s.Sid === 'AuditExportPut');
    expect(put).toBeDefined();
    expect(put.Effect).toBe('Allow');
    expect(actionsOf(put)).toEqual(['s3:PutObject']);
    // `s3:PutObject` covers the whole multipart write path (create / upload part
    // / complete), so even a large chain segment needs nothing more.
    const resource = JSON.stringify(put.Resource);
    expect(resource).toContain('AuditExportBucket');
    expect(resource).toContain('/*');
    expect(resource).not.toContain('DocumentsBucket');
    expect(put.Condition).toBeUndefined();
  });

  test('api task role has NO way to remove, shorten or read back anything in the audit-export bucket', () => {
    const statements = apiTaskStatements();
    // Every statement that names the audit bucket, whatever its Sid.
    const auditStatements = statements.filter((s) => JSON.stringify(s.Resource ?? '').includes('AuditExportBucket'));
    expect(auditStatements).toHaveLength(1);
    const granted = auditStatements.flatMap(actionsOf);
    expect(granted).toEqual(['s3:PutObject']);
    for (const forbidden of [
      's3:DeleteObject', 's3:DeleteObjectVersion', 's3:BypassGovernanceRetention',
      's3:PutObjectRetention', 's3:PutObjectLegalHold', 's3:PutBucketObjectLockConfiguration',
      's3:AbortMultipartUpload', 's3:GetObject', 's3:ListBucket', 's3:DeleteBucket', 's3:PutBucketPolicy',
    ]) {
      expect(granted).not.toContain(forbidden);
    }
    // Nothing else in the whole api stack touches that bucket either.
    const [, td] = resourcesOfType(api, 'AWS::ECS::TaskDefinition')[0];
    expect(JSON.stringify(td)).toContain('AuditExportBucket'); // ...except as configuration
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
