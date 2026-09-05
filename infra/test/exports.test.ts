import * as fs from 'fs';
import * as path from 'path';
import * as contract from './contract';
import * as exportsModule from '../lib/exports';

/** lib/exports.ts (shared verbatim with zentax-ui/infra) says exactly what ADR-0024 says. */
describe('lib/exports.ts (the shared export contract)', () => {
  test('per-environment key lists match the contract, in the same order', () => {
    expect([...exportsModule.NETWORK_EXPORTS]).toEqual([...contract.NETWORK_EXPORTS]);
    expect([...exportsModule.DATA_EXPORTS]).toEqual([...contract.DATA_EXPORTS]);
    expect([...exportsModule.CLUSTER_EXPORTS]).toEqual([...contract.CLUSTER_EXPORTS]);
    expect([...exportsModule.API_EXPORTS]).toEqual([...contract.API_EXPORTS]);
    expect([...exportsModule.WEB_EXPORTS]).toEqual([...contract.WEB_EXPORTS]);
    expect([...exportsModule.EXPORT_KEYS]).toEqual([
      ...contract.NETWORK_EXPORTS, ...contract.DATA_EXPORTS, ...contract.CLUSTER_EXPORTS, ...contract.API_EXPORTS, ...contract.WEB_EXPORTS,
    ]);
    expect(new Set(exportsModule.EXPORT_KEYS).size).toBe(exportsModule.EXPORT_KEYS.length);
  });

  test('exportName renders zentax-<env>-<key>; the OIDC names are account-level', () => {
    expect(exportsModule.exportName('staging', 'vpc-id')).toBe('zentax-staging-vpc-id');
    expect(exportsModule.exportName('production', 'api-internal-url')).toBe('zentax-production-api-internal-url');
    for (const key of exportsModule.EXPORT_KEYS) {
      for (const env of ['staging', 'production']) {
        expect(exportsModule.exportName(env, key)).toBe(contract.exportName(env, key));
        expect(exportsModule.exportName(env, key)).toMatch(/^[A-Za-z0-9-]+$/); // valid CloudFormation export name
      }
    }
    expect(exportsModule.OIDC_EXPORTS.cfnExecutionPolicyArn).toBe('zentax-cfn-execution-policy-arn');
    expect(exportsModule.OIDC_EXPORTS.deployRoleArn('staging', 'api')).toBe('zentax-deploy-role-staging-api');
    expect(exportsModule.OIDC_EXPORTS.deployRoleArn('production', 'web')).toBe('zentax-deploy-role-production-web');
  });

  test('the file is byte-identical to the UI app\'s copy when that checkout is present next to this one', () => {
    // Local-only check (CI of one repo cannot see the other): the same file
    // must live in both apps, so compare when ../../zentax-ui exists.
    const mine = path.join(__dirname, '..', 'lib', 'exports.ts');
    const theirs = path.resolve(__dirname, '..', '..', '..', 'zentax-ui', 'infra', 'lib', 'exports.ts');
    if (!fs.existsSync(theirs)) return;
    expect(fs.readFileSync(mine, 'utf8')).toBe(fs.readFileSync(theirs, 'utf8'));
  });
});
