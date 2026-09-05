import { Match } from 'aws-cdk-lib/assertions';
import { CLUSTER_EXPORTS, exportName } from '../lib/exports';
import { ENVS, exportNamesOf, resourcesOfType, synthEnv, taskDefinition } from './helpers';

describe.each(ENVS)('ZenTax-%s-Cluster', (env) => {
  const { cluster } = synthEnv(env);

  test('cluster zentax-<env> has Container Insights and the zentax-<env>.local Cloud Map namespace', () => {
    cluster.hasResourceProperties('AWS::ECS::Cluster', {
      ClusterName: `zentax-${env}`,
      ClusterSettings: Match.arrayWith([{ Name: 'containerInsights', Value: 'enabled' }]),
    });
    cluster.hasResourceProperties('AWS::ServiceDiscovery::PrivateDnsNamespace', { Name: `zentax-${env}.local` });
    cluster.hasOutput('ClusterName', { Export: { Name: `zentax-${env}-cluster-name` } });
  });

  test('migrate task definition follows the migrate.sh contract; every credential is a secret', () => {
    const td = taskDefinition(cluster, '-migrate');
    expect(td.Properties.Family).toBe(`zentax-${env}-migrate`);
    const migrate = td.Properties.ContainerDefinitions[0];
    const envNames = migrate.Environment.map((e: any) => e.Name);
    expect(envNames).toEqual(expect.arrayContaining(['PGHOST', 'PGPORT', 'PGDATABASE', 'APP_DB_USER']));
    const byName = Object.fromEntries(migrate.Environment.map((e: any) => [e.Name, e.Value]));
    expect(byName.PGDATABASE).toBe('zentax');
    expect(byName.APP_DB_USER).toBe('zentax_app');
    expect(migrate.Secrets.map((s: any) => s.Name).sort()).toEqual(['APP_DB_PASSWORD', 'PGPASSWORD', 'PGUSER']);
    for (const s of migrate.Secrets) expect(s.ValueFrom).toBeDefined();
    expect(envNames).not.toContain('PGPASSWORD');
    expect(envNames).not.toContain('APP_DB_PASSWORD');
    // Image URI is assembled from the Data stack's MigrateRepo reference (cross-stack).
    expect(JSON.stringify(migrate.Image)).toContain('MigrateRepo');
  });

  test('seed task definition runs seed-admin from the api image with the full api contract + SEED_ADMIN_PASSWORD secret', () => {
    const td = taskDefinition(cluster, '-seed');
    const seed = td.Properties.ContainerDefinitions[0];
    expect(seed.EntryPoint).toEqual(['seed-admin']);
    expect(JSON.stringify(seed.Image)).toContain('ApiRepo');
    const envNames = seed.Environment.map((e: any) => e.Name);
    expect(envNames).toEqual(
      expect.arrayContaining([
        'APP_ENV', 'DB_HOST', 'DB_PORT', 'DB_NAME', 'DB_USER', 'DB_SSLMODE', 'LOG_FORMAT',
        'CORS_ALLOWED_ORIGINS', 'STORAGE_DRIVER', 'STORAGE_S3_BUCKET', 'STORAGE_S3_REGION',
      ]),
    );
    const byName = Object.fromEntries(seed.Environment.map((e: any) => [e.Name, e.Value]));
    expect(byName.CORS_ALLOWED_ORIGINS).toBe('https://seed.invalid');
    expect(byName.APP_ENV).toBe(env);
    // The seed CLI builds no links: PUBLIC_BASE_URL is the api service's alone.
    expect(envNames).not.toContain('PUBLIC_BASE_URL');
    expect(byName.STORAGE_DRIVER).toBe('s3');
    expect(byName.STORAGE_S3_REGION).toBe('eu-central-1');
    expect(JSON.stringify(byName.STORAGE_S3_BUCKET)).toContain('DocumentsBucket');
    expect(envNames).not.toContain('DB_PASSWORD');
    expect(envNames).not.toContain('SEED_ADMIN_PASSWORD');
    expect(seed.Secrets.map((s: any) => s.Name).sort()).toEqual(['AUTH_ENCRYPTION_KEY', 'DB_PASSWORD', 'SEED_ADMIN_PASSWORD']);
    const seedPassword = seed.Secrets.find((s: any) => s.Name === 'SEED_ADMIN_PASSWORD');
    expect(seedPassword.ValueFrom).toBeDefined();
    expect(seedPassword.Value).toBeUndefined();
    expect(JSON.stringify(seedPassword.ValueFrom)).toContain('SeedAdminSecret');
    // Nothing that looks like a password ever lands in the task definition's command.
    expect(JSON.stringify(seed.Command ?? [])).not.toMatch(/--password/);
    cluster.hasOutput('SeedAdminSecretArn', { Export: { Name: `zentax-${env}-seed-admin-secret-arn` } });
  });

  test('both jobs log to their own /zentax/<env>/* log group and export families + pinned-revision ARNs for CI', () => {
    for (const [, td] of resourcesOfType(cluster, 'AWS::ECS::TaskDefinition')) {
      const logging = td.Properties.ContainerDefinitions[0].LogConfiguration;
      expect(logging.LogDriver).toBe('awslogs');
      expect(logging.Options['awslogs-group']).toBeDefined();
    }
    cluster.resourceCountIs('AWS::ECS::TaskDefinition', 2);
    cluster.resourceCountIs('AWS::ECS::Service', 0);
    cluster.hasOutput('MigrateTaskDefinitionFamily', { Export: { Name: `zentax-${env}-migrate-task-family` } });
    cluster.hasOutput('SeedTaskDefinitionFamily', { Export: { Name: `zentax-${env}-seed-task-family` } });
    cluster.hasOutput('MigrateTaskDefinitionArn', { Export: { Name: `zentax-${env}-migrate-task-arn` }, Value: { Ref: Match.stringLikeRegexp('^MigrateTask') } });
    cluster.hasOutput('SeedTaskDefinitionArn', { Export: { Name: `zentax-${env}-seed-task-arn` }, Value: { Ref: Match.stringLikeRegexp('^SeedTask') } });
  });

  test('exports the whole Cluster part of the contract, including the cluster ARN and the Cloud Map namespace the Web stack joins', () => {
    const names = exportNamesOf(cluster);
    for (const key of CLUSTER_EXPORTS) expect(names).toContain(exportName(env, key));
    cluster.hasOutput('ClusterArn', { Export: { Name: `zentax-${env}-cluster-arn` }, Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Cluster'), 'Arn'] } });
    cluster.hasOutput('NamespaceName', { Export: { Name: `zentax-${env}-namespace-name` }, Value: `zentax-${env}.local` });
    cluster.hasOutput('NamespaceId', { Export: { Name: `zentax-${env}-namespace-id` }, Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('Namespace'), 'Id'] } });
    cluster.hasOutput('NamespaceArn', { Export: { Name: `zentax-${env}-namespace-arn` }, Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('Namespace'), 'Arn'] } });
  });

  test('no literal secret values in the template', () => {
    const text = JSON.stringify(cluster.toJSON());
    expect(text).not.toMatch(/"(DB_PASSWORD|AUTH_ENCRYPTION_KEY|PGPASSWORD|APP_DB_PASSWORD|SEED_ADMIN_PASSWORD)"\s*,\s*"Value"/);
  });
});
