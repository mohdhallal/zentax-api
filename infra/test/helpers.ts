import * as cdk from 'aws-cdk-lib';
import { Template } from 'aws-cdk-lib/assertions';
import * as fs from 'fs';
import * as path from 'path';
import { EnvName } from '../lib/config';
import { addEnvironment, addGithubOidc, EnvironmentStacks } from '../lib/zentax-app';

/** The real cdk.json context, so the tests exercise the committed configuration. */
export function cdkJsonContext(): Record<string, unknown> {
  const raw = fs.readFileSync(path.join(__dirname, '..', 'cdk.json'), 'utf8');
  return JSON.parse(raw).context;
}

export interface SynthesizedEnv {
  readonly stacks: EnvironmentStacks;
  readonly network: Template;
  readonly data: Template;
  readonly cluster: Template;
  readonly api: Template;
}

const cache = new Map<EnvName, SynthesizedEnv>();

function synthesize(env: EnvName, context: Record<string, unknown>): SynthesizedEnv {
  const app = new cdk.App({ context });
  const stacks = addEnvironment(app, env);
  return {
    stacks,
    network: Template.fromStack(stacks.network),
    data: Template.fromStack(stacks.data),
    cluster: Template.fromStack(stacks.cluster),
    api: Template.fromStack(stacks.api),
  };
}

export function synthEnv(env: EnvName): SynthesizedEnv {
  const cached = cache.get(env);
  if (cached) return cached;
  const result = synthesize(env, cdkJsonContext());
  cache.set(env, result);
  return result;
}

/**
 * Synthesize with modified context (uncached): `topLevel` adds/overrides
 * top-level context keys (e.g. `imageTag` from the CLI), `envOverrides`
 * patches `environments.<env>`.
 */
export function synthEnvWith(
  env: EnvName,
  opts: { topLevel?: Record<string, unknown>; envOverrides?: Record<string, unknown> },
): SynthesizedEnv {
  const context = cdkJsonContext();
  const environments = { ...(context.environments as Record<string, Record<string, unknown>>) };
  environments[env] = { ...environments[env], ...(opts.envOverrides ?? {}) };
  return synthesize(env, { ...context, ...(opts.topLevel ?? {}), environments });
}

let oidcTemplate: Template | undefined;
export function synthOidc(): Template {
  if (!oidcTemplate) {
    const app = new cdk.App({ context: cdkJsonContext() });
    oidcTemplate = Template.fromStack(addGithubOidc(app));
  }
  return oidcTemplate;
}

/** All resources of the given types as [logicalId, resource] pairs. */
export function resourcesOfType(template: Template, ...types: string[]): Array<[string, any]> {
  const json = template.toJSON();
  return Object.entries(json.Resources ?? {}).filter(([, r]: [string, any]) => types.includes(r.Type)) as Array<[string, any]>;
}

/** Find the ECS task definition whose Family ends with the suffix. */
export function taskDefinition(template: Template, familySuffix: string): any {
  const match = resourcesOfType(template, 'AWS::ECS::TaskDefinition').find(([, r]) =>
    String(r.Properties.Family).endsWith(familySuffix),
  );
  if (!match) throw new Error(`no task definition with family *${familySuffix}`);
  return match[1];
}

/** The environment's templates by stack short name (deployment order). */
export function envTemplates(synth: SynthesizedEnv): Record<string, Template> {
  return { Network: synth.network, Data: synth.data, Cluster: synth.cluster, Api: synth.api };
}

/** The CloudFormation export names a template declares (Outputs[*].Export.Name). */
export function exportNamesOf(template: Template): string[] {
  const outputs = (template.toJSON().Outputs ?? {}) as Record<string, any>;
  return Object.values(outputs).map((o) => o.Export?.Name).filter((n): n is string => typeof n === 'string');
}

export const ENVS: EnvName[] = ['staging', 'production'];
