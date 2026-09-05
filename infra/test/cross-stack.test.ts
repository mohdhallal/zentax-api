import * as cdk from 'aws-cdk-lib';
import { configuredEnvNames, envTitle, isEnvName, loadEnvConfig, WEB_ONLY_KEYS } from '../lib/config';
import { API_EXPORTS, CLUSTER_EXPORTS, DATA_EXPORTS, ENV_EXPORTS_BY_STACK, NETWORK_EXPORTS, WEB_EXPORTS, exportName } from './contract';
import { cdkJsonContext, ENVS, envTemplates, exportNamesOf, otherEnv, resourcesOfType, synthEnv, synthEnvWith, TIER_OF, TITLE_OF } from './helpers';

/** Rules that hold across every stack of an environment. */
describe.each(ENVS)('ZenTax-%s (all API-owned stacks)', (env) => {
  const synth = synthEnv(env);
  const templates = envTemplates(synth);

  test('the four stacks are ZenTax-<Title>-{Network,Data,Cluster,Api} (Title = PascalCase cell name) — in eu-central-1, tagged Tier + RegionLabel, no Web/Edge/App here', () => {
    expect(Object.keys(templates)).toEqual(['Network', 'Data', 'Cluster', 'Api']);
    expect(synth.stacks.cfg.title).toBe(TITLE_OF[env]);
    expect(synth.stacks.cfg.tier).toBe(TIER_OF[env]);
    expect(synth.stacks.cfg.regionLabel).toBe('eu');
    for (const [short, stack] of [['Network', synth.stacks.network], ['Data', synth.stacks.data], ['Cluster', synth.stacks.cluster], ['Api', synth.stacks.api]] as const) {
      expect(stack.region).toBe('eu-central-1');
      expect(stack.stackName).toBe(`ZenTax-${TITLE_OF[env]}-${short}`);
      expect(stack.tags.tagValues()).toEqual({ Project: 'ZenTax', Environment: env, Tier: TIER_OF[env], RegionLabel: 'eu', ManagedBy: 'cdk' });
    }
    // The old tier-only stack names are gone.
    expect(JSON.stringify(Object.values(templates).map((t) => t.toJSON()))).not.toMatch(/ZenTax-(Staging|Production)-/);
  });

  test('every derived name follows the cell name (cluster, namespace, ECR, logs, secrets, KMS alias, bucket, task roles), never the bare tier', () => {
    const all = JSON.stringify(Object.values(templates).map((t) => t.toJSON()));
    expect(all).toContain(`"zentax-${env}"`); // cluster / VPC / RDS identifier
    expect(all).toContain(`zentax-${env}.local`);
    expect(all).toContain(`zentax/${env}/api`);
    expect(all).toContain(`/zentax/${env}/api`);
    expect(all).toContain(`zentax/${env}/app-db`);
    expect(all).toContain(`alias/zentax/${env}/data`);
    expect(all).toContain(`zentax-${env}-documents-160117555326-eu-central-1`);
    expect(all).toContain(`zentax-${env}-api-task`);
    expect(all).not.toContain(`zentax-${otherEnv(env)}`);
    // "zentax-staging" / "zentax/production" only ever appear followed by "-eu" (the region label).
    expect(all).not.toMatch(/zentax[-/](staging|production)(?!-[a-z]{2}[-/."])/);
    // S3 bucket names are capped at 63 characters.
    expect(`zentax-${env}-documents-160117555326-eu-central-1`.length).toBeLessThanOrEqual(63);
  });

  test('no log group in any template lacks RetentionInDays', () => {
    let count = 0;
    for (const [stack, template] of Object.entries(templates)) {
      for (const [id, lg] of resourcesOfType(template, 'AWS::Logs::LogGroup')) {
        count++;
        expect({ stack, id, retention: lg.Properties.RetentionInDays }).toEqual(
          expect.objectContaining({ retention: expect.any(Number) }),
        );
      }
    }
    // 2 (Network) + 4 (Data: api, migrate, seed, RDS export) + RDSOSMetrics in
    // the owning environment (staging-eu). The web log group is the UI app's.
    expect(count).toBe(env === 'staging-eu' ? 7 : 6);
  });

  test('`--context imageTag=<sha>` pins every container image (api, migrate, seed) to that tag', () => {
    const tag = 'deadbeef0000';
    const pinned = synthEnvWith(env, { topLevel: { imageTag: tag } });
    const images: Array<[string, unknown]> = [];
    for (const template of [pinned.cluster, pinned.api]) {
      for (const [, td] of resourcesOfType(template, 'AWS::ECS::TaskDefinition')) {
        for (const c of td.Properties.ContainerDefinitions) images.push([c.Name, c.Image]);
      }
    }
    expect(images.map(([name]) => name).sort()).toEqual(['api', 'migrate', 'seed']);
    for (const [, image] of images) {
      // Image is a Fn::Join of the repository URI parts and ":<tag>" last.
      const parts = (image as any)['Fn::Join'][1] as unknown[];
      expect(parts[parts.length - 1]).toBe(`:${tag}`);
    }
    expect(pinned.stacks.cfg.imageTag).toBe(tag);
    // Without the CLI value the per-environment default applies.
    expect(synth.stacks.cfg.imageTag).toBe('latest');
    expect(() => synthEnvWith(env, { topLevel: { imageTag: 'not a tag!' } })).toThrow(/imageTag/);
  });

  test('alarmEmail context subscribes an address to the alarm topic', () => {
    const withEmail = synthEnvWith(env, { envOverrides: { alarmEmail: 'ops@example.com' } });
    withEmail.data.hasResourceProperties('AWS::SNS::Subscription', { Protocol: 'email', Endpoint: 'ops@example.com' });
    synth.data.resourceCountIs('AWS::SNS::Subscription', 0);
    expect(() => synthEnvWith(env, { envOverrides: { alarmEmail: 'nope' } })).toThrow(/alarmEmail/);
  });

  test('web-only context keys are refused (they belong to zentax-ui/infra and would silently do nothing here)', () => {
    const sample: Record<string, unknown> = {
      webDesiredCount: 1, originVerifyVersion: 1, maxUploadBytes: 1,
      domainName: 'x', certificateArn: 'x', hostedZoneId: 'x', hostedZoneName: 'x', entryHostnames: ['app.example'],
    };
    expect([...WEB_ONLY_KEYS].sort()).toEqual(Object.keys(sample).sort());
    for (const key of WEB_ONLY_KEYS) {
      expect(() => synthEnvWith(env, { envOverrides: { [key]: sample[key] } })).toThrow(new RegExp(`${key}.*zentax-ui/infra`));
    }
  });

  test('cell identity: tier + regionLabel are explicit in cdk.json and must agree with the key; publicHostname is validated', () => {
    expect(() => synthEnvWith(env, { envOverrides: { tier: TIER_OF[otherEnv(env)] } })).toThrow(/the key must be/);
    expect(() => synthEnvWith(env, { envOverrides: { regionLabel: 'us' } })).toThrow(/the key must be/);
    expect(() => synthEnvWith(env, { envOverrides: { regionLabel: 'eur' } })).toThrow(/regionLabel must be two lowercase letters/);
    expect(() => synthEnvWith(env, { envOverrides: { tier: 'prod' } })).toThrow(/tier must be "staging" or "production"/);
    expect(() => synthEnvWith(env, { envOverrides: { tier: undefined } })).toThrow(/"tier" must be a non-empty string/);
    expect(() => synthEnvWith(env, { envOverrides: { publicHostname: 'https://eu.staging.zentax.software' } })).toThrow(/publicHostname/);
    expect(() => synthEnvWith(env, { envOverrides: { publicHostname: 'EU.staging.zentax.software' } })).toThrow(/publicHostname/);
    // Omitting the hostname is allowed (custom domain off): the api falls back to the .invalid placeholder.
    const noHost = synthEnvWith(env, { envOverrides: { publicHostname: '' } });
    expect(noHost.stacks.cfg.publicHostname).toBeUndefined();
    expect(noHost.stacks.cfg.corsAllowedOrigins).toBe(`https://zentax-${env}.invalid`);
  });

  // ---- The split ------------------------------------------------------------

  test('every export of the contract is emitted, with the exact name, by the stack the contract assigns it to', () => {
    const emitted: Record<string, string[]> = Object.fromEntries(
      Object.entries(templates).map(([stack, template]) => [stack, exportNamesOf(template)]),
    );
    for (const [stack, keys] of Object.entries(ENV_EXPORTS_BY_STACK)) {
      for (const key of keys) {
        expect({ stack, export: exportName(env, key), present: emitted[stack].includes(exportName(env, key)) })
          .toEqual(expect.objectContaining({ present: true }));
      }
    }
    // Nothing of the Web part is emitted by the API app, and no two stacks export the same name.
    const all = Object.values(emitted).flat();
    for (const key of WEB_EXPORTS) expect(all).not.toContain(exportName(env, key));
    expect(new Set(all).size).toBe(all.length);
    // The complete per-environment list, spelled out (the contract in ADR-0024).
    expect(all.filter((n) => n.startsWith(`zentax-${env}-`)).sort()).toEqual(
      [...NETWORK_EXPORTS, ...DATA_EXPORTS, ...CLUSTER_EXPORTS, ...API_EXPORTS].map((k) => exportName(env, k)).sort(),
    );
  });

  test('no stack imports anything from the UI app (no Fn::ImportValue of a Web export) and none references a Web/Edge/App stack', () => {
    for (const [stack, template] of Object.entries(templates)) {
      const text = JSON.stringify(template.toJSON());
      expect({ stack, importsWeb: /Fn::ImportValue/.test(text) }).toEqual(expect.objectContaining({ importsWeb: false }));
      expect(text).not.toMatch(new RegExp(`ZenTax-${synth.stacks.cfg.title}-(Web|Edge|App)`));
    }
  });

  test('no security-group ingress from 0.0.0.0/0 or ::/0 in any template', () => {
    for (const template of Object.values(templates)) {
      for (const [, g] of resourcesOfType(template, 'AWS::EC2::SecurityGroup')) {
        for (const rule of g.Properties.SecurityGroupIngress ?? []) {
          expect(rule.CidrIp).not.toBe('0.0.0.0/0');
          expect(rule.CidrIpv6).not.toBe('::/0');
        }
      }
      for (const [, r] of resourcesOfType(template, 'AWS::EC2::SecurityGroupIngress')) {
        expect(r.Properties.CidrIp).not.toBe('0.0.0.0/0');
        expect(r.Properties.CidrIpv6).not.toBe('::/0');
      }
    }
  });

  test('every task definition carries its secrets as ValueFrom only, and never a Value', () => {
    let secrets = 0;
    for (const template of Object.values(templates)) {
      for (const [, td] of resourcesOfType(template, 'AWS::ECS::TaskDefinition')) {
        for (const c of td.Properties.ContainerDefinitions) {
          for (const s of c.Secrets ?? []) {
            secrets++;
            expect(Object.keys(s).sort()).toEqual(['Name', 'ValueFrom']);
            expect(JSON.stringify(s.ValueFrom)).toMatch(/secret|Secret/);
          }
          for (const e of c.Environment ?? []) {
            expect(e.Name).not.toMatch(/PASSWORD|SECRET|ENCRYPTION_KEY|TOKEN/);
          }
        }
      }
    }
    expect(secrets).toBe(3 + 3 + 2); // migrate + seed + api
  });
});

describe('cell names (tier + region label)', () => {
  test('cdk.json declares exactly staging-eu and production-eu, each with matching tier/regionLabel and its public hostname', () => {
    const app = new cdk.App({ context: cdkJsonContext() });
    expect(configuredEnvNames(app)).toEqual(['staging-eu', 'production-eu']);
    expect(loadEnvConfig(app, 'staging-eu')).toEqual(expect.objectContaining({
      name: 'staging-eu', tier: 'staging', regionLabel: 'eu', title: 'StagingEu', publicHostname: 'eu.staging.zentax.software',
    }));
    expect(loadEnvConfig(app, 'production-eu')).toEqual(expect.objectContaining({
      name: 'production-eu', tier: 'production', regionLabel: 'eu', title: 'ProductionEu', publicHostname: 'eu.app.zentax.software',
    }));
  });

  test('isEnvName = well-formed AND declared in cdk.json; the title is PascalCase of the name', () => {
    const app = new cdk.App({ context: cdkJsonContext() });
    expect(isEnvName(app, 'staging-eu')).toBe(true);
    expect(isEnvName(app, 'production-eu')).toBe(true);
    for (const bad of ['staging', 'production', 'staging-us', 'staging-eu1', 'Staging-EU', 'dev-eu', '', undefined, 3]) {
      expect(isEnvName(app, bad)).toBe(false);
    }
    expect(envTitle('staging-eu')).toBe('StagingEu');
    expect(envTitle('production-eu')).toBe('ProductionEu');
    expect(envTitle('staging-us')).toBe('StagingUs');
    expect(() => loadEnvConfig(app, 'staging-us')).toThrow(/environments\.staging-us is missing/);
    expect(() => loadEnvConfig(app, 'staging' as any)).toThrow(/not a cell name/);
  });

  test('a stray tier-only key in cdk.json fails synth (every key must be <tier>-<regionLabel>)', () => {
    const context = cdkJsonContext();
    const environments = context.environments as Record<string, unknown>;
    const legacy = new cdk.App({ context: { ...context, environments: { ...environments, production: environments['production-eu'] } } });
    expect(() => configuredEnvNames(legacy)).toThrow(/environments\.production: cell names are/);
    expect(() => isEnvName(legacy, 'staging-eu')).toThrow(/cell names are/);
  });
});
