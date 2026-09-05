import * as fs from 'fs';
import * as path from 'path';
import * as contract from './contract';
import * as exportsModule from '../lib/exports';
import { ENVS } from './helpers';

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

  test('exportName renders zentax-<cell>-<key> for the tier+region-label cell names; the OIDC names are account-level', () => {
    expect(exportsModule.exportName('staging-eu', 'vpc-id')).toBe('zentax-staging-eu-vpc-id');
    expect(exportsModule.exportName('production-eu', 'api-internal-url')).toBe('zentax-production-eu-api-internal-url');
    for (const key of exportsModule.EXPORT_KEYS) {
      for (const env of ENVS) {
        expect(exportsModule.exportName(env, key)).toBe(contract.exportName(env, key));
        expect(exportsModule.exportName(env, key)).toMatch(/^[A-Za-z0-9-]+$/); // valid CloudFormation export name
      }
    }
    expect(exportsModule.OIDC_EXPORTS.cfnExecutionPolicyArn).toBe('zentax-cfn-execution-policy-arn');
    expect(exportsModule.OIDC_EXPORTS.deployRoleArn('staging-eu', 'api')).toBe('zentax-deploy-role-staging-eu-api');
    expect(exportsModule.OIDC_EXPORTS.deployRoleArn('production-eu', 'web')).toBe('zentax-deploy-role-production-eu-web');
    // The Edge execution-policy export is NOT part of the shared file (nothing imports it).
    expect(JSON.stringify(exportsModule.OIDC_EXPORTS)).not.toContain('edge');
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
