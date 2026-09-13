import { Match } from 'aws-cdk-lib/assertions';
import { DATA_EXPORTS, exportName } from '../lib/exports';
import { ENVS, exportNamesOf, resourcesOfType, synthEnv, synthEnvWith } from './helpers';

const expected = {
  'staging-eu': {
    multiAz: false, deletionProtection: false, backupDays: 7, retention: 30,
    dataDeletionPolicy: 'Delete', dbDeletionPolicy: 'Delete', deleteAutomatedBackups: true,
    allocatedGb: 20, classMemoryGib: 2, ownsRdsOsMetrics: true,
    // The WORM hold: one day in staging (synthetic data), ten years in production.
    auditLockDays: 1,
  },
  'production-eu': {
    multiAz: true, deletionProtection: true, backupDays: 35, retention: 365,
    dataDeletionPolicy: 'Retain', dbDeletionPolicy: 'Snapshot', deleteAutomatedBackups: false,
    allocatedGb: 50, classMemoryGib: 4, ownsRdsOsMetrics: false,
    auditLockDays: 3653,
  },
} as const;

describe.each(ENVS)('ZenTax-%s-Data', (env) => {
  const want = expected[env];
  const production = env === 'production-eu';
  const { data } = synthEnv(env);

  test('RDS is Postgres 16, KMS-encrypted, with Multi-AZ / deletion protection / backups per environment', () => {
    data.hasResourceProperties('AWS::RDS::DBInstance', {
      Engine: 'postgres',
      EngineVersion: Match.stringLikeRegexp('^16'),
      DBName: 'zentax',
      StorageEncrypted: true,
      KmsKeyId: Match.anyValue(),
      MultiAZ: want.multiAz,
      DeletionProtection: want.deletionProtection,
      BackupRetentionPeriod: want.backupDays,
      EnablePerformanceInsights: true,
      EnableCloudwatchLogsExports: ['postgresql'],
      PubliclyAccessible: false,
      DBInstanceClass: production ? 'db.t4g.medium' : 'db.t4g.small',
      DBInstanceIdentifier: `zentax-${env}`,
    });
  });

  test(`RDS removal: ${want.dbDeletionPolicy} on delete, deleteAutomatedBackups=${want.deleteAutomatedBackups}, deletionProtection=${want.deletionProtection} (three independent guards)`, () => {
    data.hasResource('AWS::RDS::DBInstance', {
      DeletionPolicy: want.dbDeletionPolicy,
      UpdateReplacePolicy: want.dbDeletionPolicy,
      Properties: Match.objectLike({ DeleteAutomatedBackups: want.deleteAutomatedBackups, DeletionProtection: want.deletionProtection }),
    });
    // Production is never plain-deleted: a final snapshot is the worst case.
    if (production) {
      const [, db] = resourcesOfType(data, 'AWS::RDS::DBInstance')[0];
      expect(db.DeletionPolicy).not.toBe('Delete');
    }
  });

  test('RDS parameter group forces SSL and never logs bind parameters', () => {
    data.hasResourceProperties('AWS::RDS::DBParameterGroup', {
      Family: 'postgres16',
      Parameters: Match.objectLike({
        'rds.force_ssl': '1',
        'log_parameter_max_length': '0',
        'log_parameter_max_length_on_error': '0',
      }),
    });
  });

  test('master credentials are a generated Secrets Manager secret, never a literal', () => {
    const [, db] = resourcesOfType(data, 'AWS::RDS::DBInstance')[0];
    expect(JSON.stringify(db.Properties.MasterUserPassword)).toContain('{{resolve:secretsmanager:');
    data.hasResourceProperties('AWS::SecretsManager::Secret', {
      Name: `zentax/${env}/db-master`,
      GenerateSecretString: Match.objectLike({ GenerateStringKey: 'password' }),
    });
  });

  test('app-db, auth-encryption-key and seed-admin secrets are generated with the agreed shapes; origin-verify is the Web stack\'s', () => {
    data.hasResourceProperties('AWS::SecretsManager::Secret', {
      Name: `zentax/${env}/app-db`,
      GenerateSecretString: Match.objectLike({
        SecretStringTemplate: '{"username":"zentax_app"}',
        GenerateStringKey: 'password',
        ExcludePunctuation: true,
      }),
    });
    data.hasResourceProperties('AWS::SecretsManager::Secret', {
      Name: `zentax/${env}/auth-encryption-key`,
      GenerateSecretString: Match.objectLike({ PasswordLength: 64, ExcludePunctuation: true }),
    });
    const [, seedAdmin] = resourcesOfType(data, 'AWS::SecretsManager::Secret').find(
      ([, r]) => r.Properties.Name === `zentax/${env}/seed-admin`,
    )!;
    expect(seedAdmin.Properties.GenerateSecretString.PasswordLength).toBeGreaterThanOrEqual(24);
    expect(seedAdmin.Properties.GenerateSecretString.ExcludePunctuation).toBe(true);
    expect(seedAdmin.Properties.SecretString).toBeUndefined();
    expect(seedAdmin.DeletionPolicy).toBe(want.dataDeletionPolicy);
    // The CloudFront -> ALB shared secret moved to the UI app with the ALB.
    const names = resourcesOfType(data, 'AWS::SecretsManager::Secret').map(([, r]) => r.Properties.Name);
    expect(names.sort()).toEqual([`zentax/${env}/app-db`, `zentax/${env}/auth-encryption-key`, `zentax/${env}/db-master`, `zentax/${env}/seed-admin`]);
    expect(JSON.stringify(data.toJSON())).not.toContain('origin-verify');
  });

  test('KMS key has rotation enabled and the zentax/<env>/data alias', () => {
    data.hasResourceProperties('AWS::KMS::Key', { EnableKeyRotation: true });
    data.hasResourceProperties('AWS::KMS::Alias', { AliasName: `alias/zentax/${env}/data` });
  });

  test(`documents bucket blocks public access, is versioned and KMS-encrypted, enforces TLS, expires old versions after dbBackupDays=${want.backupDays}`, () => {
    data.hasResourceProperties('AWS::S3::Bucket', {
      BucketName: Match.stringLikeRegexp(`^zentax-${env}-documents-`),
      PublicAccessBlockConfiguration: {
        BlockPublicAcls: true,
        BlockPublicPolicy: true,
        IgnorePublicAcls: true,
        RestrictPublicBuckets: true,
      },
      VersioningConfiguration: { Status: 'Enabled' },
      BucketEncryption: {
        ServerSideEncryptionConfiguration: [
          Match.objectLike({ ServerSideEncryptionByDefault: Match.objectLike({ SSEAlgorithm: 'aws:kms' }) }),
        ],
      },
      LifecycleConfiguration: {
        Rules: Match.arrayWith([
          Match.objectLike({ NoncurrentVersionExpiration: { NoncurrentDays: want.backupDays } }),
          Match.objectLike({ AbortIncompleteMultipartUpload: { DaysAfterInitiation: 7 } }),
        ]),
      },
    });
    data.hasResourceProperties('AWS::S3::BucketPolicy', {
      PolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Effect: 'Deny',
            Action: 's3:*',
            Condition: Match.objectLike({ Bool: { 'aws:SecureTransport': 'false' } }),
          }),
        ]),
      },
    });
    data.hasResource('AWS::S3::Bucket', {
      Properties: Match.objectLike({ BucketName: Match.stringLikeRegexp('documents') }),
      DeletionPolicy: want.dataDeletionPolicy,
    });
    // Documents + the WORM audit export; the ALB access-log bucket is the Web stack's.
    data.resourceCountIs('AWS::S3::Bucket', 2);
    const names = resourcesOfType(data, 'AWS::S3::Bucket').map(([, r]) => r.Properties.BucketName).sort();
    expect(names).toEqual([
      `zentax-${env}-audit-export-160117555326-eu-central-1`,
      `zentax-${env}-documents-160117555326-eu-central-1`,
    ]);
    // The documents bucket is NOT the WORM one: the api deletes documents, so
    // an Object Lock here would break the product (and could not be added later
    // without recreating the bucket).
    const [, documents] = resourcesOfType(data, 'AWS::S3::Bucket').find(([, r]) => String(r.Properties.BucketName).includes('-documents-'))!;
    expect(documents.Properties.ObjectLockEnabled).toBeUndefined();
    expect(documents.Properties.ObjectLockConfiguration).toBeUndefined();
  });

  // ---- ADR-0008: the WORM copy of the audit hash chain ----------------------

  const auditBucket = () =>
    resourcesOfType(data, 'AWS::S3::Bucket').find(([, r]) => String(r.Properties.BucketName).includes('-audit-export-'))![1];

  test(`audit-export bucket is versioned and Object-Locked in COMPLIANCE mode for ${want.auditLockDays} day(s) — the mode no administrator, root or AWS Support can bypass`, () => {
    const bucket = auditBucket();
    // Versioning is not optional: Object Lock requires it, and S3 refuses to
    // suspend it afterwards, so an overwrite can only add a version beside the
    // locked one.
    expect(bucket.Properties.VersioningConfiguration).toEqual({ Status: 'Enabled' });
    expect(bucket.Properties.ObjectLockEnabled).toBe(true);
    expect(bucket.Properties.ObjectLockConfiguration).toEqual({
      ObjectLockEnabled: 'Enabled',
      Rule: { DefaultRetention: { Mode: 'COMPLIANCE', Days: want.auditLockDays } },
    });
    // GOVERNANCE is the mode an administrator can bypass; it must not appear.
    expect(JSON.stringify(bucket)).not.toContain('GOVERNANCE');
    // A default retention is what makes the writer's single PutObject enough:
    // the hold is stamped by the bucket, not asked for by the caller.
    expect(bucket.Properties.ObjectLockConfiguration.Rule.DefaultRetention.Years).toBeUndefined();
  });

  test('audit-export bucket: production holds the statutory window (10 years incl. leap days), staging the shortest lock S3 offers', () => {
    const days = auditBucket().Properties.ObjectLockConfiguration.Rule.DefaultRetention.Days;
    if (production) {
      // Ten calendar years — deliberately not 3650, which expires two days early.
      expect(days).toBe(Math.ceil(10 * 365.25));
      // ...and never under ADR-0007's six-year floor for tax-record evidence.
      expect(days).toBeGreaterThanOrEqual(Math.ceil(6 * 365.25));
    } else {
      expect(days).toBe(1);
    }
  });

  test('audit-export bucket is retained by BOTH cells and never auto-emptied — a teardown may not take the evidence with it', () => {
    const bucket = auditBucket();
    expect(bucket.DeletionPolicy).toBe('Retain');
    expect(bucket.UpdateReplacePolicy).toBe('Retain');
    // The auto-delete custom resource deletes object versions, which COMPLIANCE
    // refuses; it must not be attached to this bucket in either cell. (Staging's
    // *documents* bucket does carry it — hence the check is per bucket.)
    expect(bucket.Properties.Tags ?? []).not.toContainEqual(
      expect.objectContaining({ Key: 'aws-cdk:auto-delete-objects' }),
    );
    const auditId = resourcesOfType(data, 'AWS::S3::Bucket').find(([, r]) => String(r.Properties.BucketName).includes('-audit-export-'))![0];
    for (const [, cr] of resourcesOfType(data, 'Custom::S3AutoDeleteObjects')) {
      expect(JSON.stringify(cr.Properties)).not.toContain(auditId);
    }
  });

  test('audit-export lifecycle expires nothing, only sweeps failed uploads and moves cold objects to Glacier Instant Retrieval', () => {
    const rules = auditBucket().Properties.LifecycleConfiguration.Rules as any[];
    expect(rules.map((r) => r.Id).sort()).toEqual(['abort-incomplete-multipart', 'archive-after-90-days']);
    for (const rule of rules) {
      expect(rule.Status).toBe('Enabled');
      // Nothing that deletes an object: a noncurrent-version expiry shorter than
      // the lock cannot fire (it only looks like a policy), and one longer would
      // erase the archive the hour its protection lapsed.
      expect(rule.ExpirationInDays).toBeUndefined();
      expect(rule.ExpirationDate).toBeUndefined();
      expect(rule.NoncurrentVersionExpiration).toBeUndefined();
      expect(rule.ExpiredObjectDeleteMarker).toBeUndefined();
    }
    const abort = rules.find((r) => r.Id === 'abort-incomplete-multipart');
    // Parts of an incomplete multipart are not objects and Object Lock does not
    // cover them — the one thing here that could accumulate unseen, and the
    // reason the api task role needs no abort permission.
    expect(abort.AbortIncompleteMultipartUpload).toEqual({ DaysAfterInitiation: 7 });
    const archive = rules.find((r) => r.Id === 'archive-after-90-days');
    expect(archive.Transitions).toEqual([{ StorageClass: 'GLACIER_IR', TransitionInDays: 90 }]);
  });

  test('audit-export bucket: KMS-encrypted with the cell key, public access blocked, TLS-only, and a second Deny on every object-removal verb', () => {
    const bucket = auditBucket();
    expect(bucket.Properties.BucketEncryption.ServerSideEncryptionConfiguration[0].ServerSideEncryptionByDefault.SSEAlgorithm).toBe('aws:kms');
    expect(bucket.Properties.BucketEncryption.ServerSideEncryptionConfiguration[0].BucketKeyEnabled).toBe(true);
    expect(JSON.stringify(bucket.Properties.BucketEncryption)).toContain('DataKey');
    expect(bucket.Properties.PublicAccessBlockConfiguration).toEqual({
      BlockPublicAcls: true, BlockPublicPolicy: true, IgnorePublicAcls: true, RestrictPublicBuckets: true,
    });
    expect(bucket.Properties.OwnershipControls.Rules).toEqual([{ ObjectOwnership: 'BucketOwnerEnforced' }]);

    const [, policy] = resourcesOfType(data, 'AWS::S3::BucketPolicy')
      .find(([id]) => id.startsWith('AuditExportBucketPolicy'))!;
    const statements = policy.Properties.PolicyDocument.Statement as any[];
    const deny = statements.find((s) => s.Sid === 'DenyObjectRemoval');
    expect(deny.Effect).toBe('Deny');
    expect(deny.Principal).toEqual({ AWS: '*' });
    expect([...deny.Action].sort()).toEqual(['s3:BypassGovernanceRetention', 's3:DeleteObject', 's3:DeleteObjectVersion']);
    expect(JSON.stringify(deny.Resource)).toContain('/*');
    // ...on top of the TLS-only statement every bucket here carries.
    expect(statements.some((s) => s.Effect === 'Deny' && s.Condition?.Bool?.['aws:SecureTransport'] === 'false')).toBe(true);
  });

  test('the audit-export bucket name is an output but deliberately not a CloudFormation export', () => {
    data.hasOutput('AuditExportBucketName', {
      Value: { Ref: Match.stringLikeRegexp('^AuditExportBucket') },
      Description: Match.stringLikeRegexp('COMPLIANCE'),
    });
    const outputs = data.toJSON().Outputs as Record<string, any>;
    expect(outputs.AuditExportBucketName.Export).toBeUndefined();
    expect(exportNamesOf(data)).not.toContain(`zentax-${env}-audit-export-bucket`);
  });

  test('auditExportRetentionDays is validated at synth: whole days >= 1, and a production cell may not go under the six-year evidence floor', () => {
    const lockDays = (days: unknown) => {
      const synth = synthEnvWith(env, { envOverrides: { auditExportRetentionDays: days } });
      const [, bucket] = resourcesOfType(synth.data, 'AWS::S3::Bucket')
        .find(([, r]) => String(r.Properties.BucketName).includes('-audit-export-'))!;
      return bucket.Properties.ObjectLockConfiguration.Rule.DefaultRetention.Days;
    };
    // A longer hold is always allowed — the number only ever moves up safely.
    expect(lockDays(4000)).toBe(4000);
    for (const bad of [0, -1, 1.5, '3653']) {
      expect(() => lockDays(bad)).toThrow(/auditExportRetentionDays/);
    }
    const sixYears = Math.ceil(6 * 365.25);
    if (production) {
      // A production cell cannot be deployed with a hold that stops covering the
      // statutory window; the archive would look fine and quietly stop being
      // evidence, so synth is the only place to catch it.
      expect(() => lockDays(sixYears - 1)).toThrow(new RegExp(`${sixYears}-day \\(6-year\\) floor`));
      expect(lockDays(sixYears)).toBe(sixYears);
    } else {
      // Staging is free to be short: it holds no evidence.
      expect(lockDays(1)).toBe(1);
    }
  });

  test('two ECR repositories (api, migrate) keep the last 30 tagged images; zentax/<env>/web is the UI app\'s', () => {
    for (const component of ['api', 'migrate']) {
      data.hasResourceProperties('AWS::ECR::Repository', {
        RepositoryName: `zentax/${env}/${component}`,
        ImageScanningConfiguration: { ScanOnPush: true },
        LifecyclePolicy: {
          LifecyclePolicyText: Match.stringLikeRegexp('"countNumber":30'),
        },
      });
    }
    data.resourceCountIs('AWS::ECR::Repository', 2);
    expect(JSON.stringify(data.toJSON())).not.toContain(`zentax/${env}/web`);
  });

  test(`log groups (api, migrate, seed, RDS export) exist with ${want.retention}-day retention; RDSOSMetrics is pre-created only by its owner (${want.ownsRdsOsMetrics}); no web log group`, () => {
    for (const component of ['api', 'migrate', 'seed']) {
      data.hasResourceProperties('AWS::Logs::LogGroup', {
        LogGroupName: `/zentax/${env}/${component}`,
        RetentionInDays: want.retention,
      });
    }
    data.hasResourceProperties('AWS::Logs::LogGroup', {
      LogGroupName: `/aws/rds/instance/zentax-${env}/postgresql`,
      RetentionInDays: want.retention,
    });
    data.allResourcesProperties('AWS::Logs::LogGroup', { RetentionInDays: want.retention });
    expect(JSON.stringify(data.toJSON())).not.toContain(`/zentax/${env}/web`);
    const osMetrics = resourcesOfType(data, 'AWS::Logs::LogGroup').filter(([, r]) => r.Properties.LogGroupName === 'RDSOSMetrics');
    expect(osMetrics).toHaveLength(want.ownsRdsOsMetrics ? 1 : 0);
    if (want.ownsRdsOsMetrics) {
      expect(osMetrics[0][1].Properties.RetentionInDays).toBe(want.retention);
      expect(osMetrics[0][1].DeletionPolicy).toBe(want.dataDeletionPolicy);
      // The instance (with enhanced monitoring) is created after the group exists.
      const [, db] = resourcesOfType(data, 'AWS::RDS::DBInstance')[0];
      expect(db.DependsOn).toContain(osMetrics[0][0]);
    }
  });

  test('alarm topic + RDS alarms (CPU, free storage, connections, freeable memory) with stated thresholds', () => {
    data.resourceCountIs('AWS::SNS::Topic', 1);
    data.hasResourceProperties('AWS::SNS::Topic', { TopicName: `zentax-${env}-alarms` });
    const alarms = resourcesOfType(data, 'AWS::CloudWatch::Alarm').map(([, r]) => r.Properties);
    expect(alarms.length).toBeGreaterThan(0);
    const byName = Object.fromEntries(alarms.map((a) => [a.AlarmName, a]));
    expect(Object.keys(byName).sort()).toEqual([
      `zentax-${env}-rds-connections`, `zentax-${env}-rds-cpu`, `zentax-${env}-rds-free-storage`, `zentax-${env}-rds-freeable-memory`,
    ]);
    for (const a of alarms) {
      expect(a.Namespace).toBe('AWS/RDS');
      expect(a.Period).toBe(300);
      expect(a.AlarmActions).toHaveLength(1);
      expect(a.OKActions).toHaveLength(1);
      expect(JSON.stringify(a.Dimensions)).toContain('DBInstanceIdentifier');
    }
    expect(byName[`zentax-${env}-rds-cpu`].Threshold).toBe(80);
    expect(byName[`zentax-${env}-rds-cpu`].ComparisonOperator).toBe('GreaterThanThreshold');
    expect(byName[`zentax-${env}-rds-free-storage`].Threshold).toBe(Math.floor(want.allocatedGb * 1024 ** 3 * 0.1));
    expect(byName[`zentax-${env}-rds-free-storage`].ComparisonOperator).toBe('LessThanThreshold');
    const maxConnections = Math.floor((want.classMemoryGib * 1024 ** 3) / 9531392);
    expect(byName[`zentax-${env}-rds-connections`].Threshold).toBe(Math.floor(maxConnections * 0.8));
    expect(byName[`zentax-${env}-rds-connections`].AlarmDescription).toContain(`max_connections=${maxConnections}`);
    expect(byName[`zentax-${env}-rds-freeable-memory`].Threshold).toBe(Math.floor(want.classMemoryGib * 1024 ** 3 * 0.1));
  });

  test('exports the Data part of the contract: alarm topic, documents bucket, api + migrate repository URIs', () => {
    const names = exportNamesOf(data);
    for (const key of DATA_EXPORTS) expect(names).toContain(exportName(env, key));
    data.hasOutput('AlarmTopicArn', { Export: { Name: `zentax-${env}-alarm-topic-arn` }, Value: { Ref: Match.stringLikeRegexp('^Alarms') } });
    data.hasOutput('DocumentsBucketName', { Export: { Name: `zentax-${env}-documents-bucket` }, Value: { Ref: Match.stringLikeRegexp('^DocumentsBucket') } });
    data.hasOutput('ApiRepositoryUri', { Export: { Name: `zentax-${env}-ecr-api` } });
    data.hasOutput('MigrateRepositoryUri', { Export: { Name: `zentax-${env}-ecr-migrate` } });
    expect(names).not.toContain(`zentax-${env}-ecr-web`);
  });

  test('no secret value is rendered into the template', () => {
    const text = JSON.stringify(data.toJSON());
    // Every secret reference must be a Secrets Manager dynamic reference or an ARN.
    expect(text).not.toMatch(/"SecretString"\s*:\s*"/);
    expect(text).not.toMatch(/"MasterUserPassword"\s*:\s*"[^{]/);
  });
});
