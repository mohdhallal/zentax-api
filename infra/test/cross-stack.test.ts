import { API_EXPORTS, CLUSTER_EXPORTS, DATA_EXPORTS, ENV_EXPORTS_BY_STACK, NETWORK_EXPORTS, WEB_EXPORTS, exportName } from './contract';
import { ENVS, envTemplates, exportNamesOf, resourcesOfType, synthEnv, synthEnvWith } from './helpers';

/** Rules that hold across every stack of an environment. */
describe.each(ENVS)('ZenTax-%s (all API-owned stacks)', (env) => {
  const synth = synthEnv(env);
  const templates = envTemplates(synth);

  test('the four stacks are Network, Data, Cluster, Api — in eu-central-1, tagged, no Web/Edge/App here', () => {
    expect(Object.keys(templates)).toEqual(['Network', 'Data', 'Cluster', 'Api']);
    for (const stack of [synth.stacks.network, synth.stacks.data, synth.stacks.cluster, synth.stacks.api]) {
      expect(stack.region).toBe('eu-central-1');
      expect(stack.stackName).toMatch(new RegExp(`^ZenTax-${synth.stacks.cfg.title}-(Network|Data|Cluster|Api)$`));
      expect(stack.tags.tagValues()).toEqual({ Project: 'ZenTax', Environment: env, ManagedBy: 'cdk' });
    }
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
    // the owning environment. The web log group is the UI app's.
    expect(count).toBe(env === 'staging' ? 7 : 6);
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
    for (const key of ['webDesiredCount', 'originVerifyVersion', 'domainName', 'certificateArn', 'hostedZoneId', 'hostedZoneName', 'maxUploadBytes']) {
      expect(() => synthEnvWith(env, { envOverrides: { [key]: key === 'webDesiredCount' || key === 'originVerifyVersion' || key === 'maxUploadBytes' ? 1 : 'x' } }))
        .toThrow(new RegExp(`${key}.*zentax-ui/infra`));
    }
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
