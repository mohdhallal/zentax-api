import * as cdk from 'aws-cdk-lib';
import * as logs from 'aws-cdk-lib/aws-logs';

/** Environment names the app knows about. Stack names use the capitalized form. */
export type EnvName = 'staging' | 'production';

export const DEFAULT_ACCOUNT = '160117555326';
export const DEFAULT_REGION = 'eu-central-1';

/**
 * Per-environment settings, read from `cdk.json` -> context.environments[env].
 * Nested keys cannot be overridden individually on the CLI (edit cdk.json);
 * the one deploy-time knob, `imageTag`, is therefore also read as a top-level
 * context value (`--context imageTag=<sha>`).
 *
 * Only what the foundations and the api service need lives here. The web
 * tier's settings (desired count, origin-verify rotation counter, custom
 * domain/certificate/hosted zone, upload limit) belong to the UI app's config.
 */
export interface EnvConfig {
  readonly name: EnvName;
  /** Stack-name segment: Staging / Production. */
  readonly title: string;
  readonly account: string;
  readonly region: string;
  readonly cidr: string;
  /** e.g. "t4g.small" -> db.t4g.small */
  readonly dbInstanceClass: string;
  readonly dbMultiAz: boolean;
  readonly dbBackupDays: number;
  readonly dbAllocatedStorageGb: number;
  readonly dbMaxAllocatedStorageGb: number;
  readonly natGateways: number;
  readonly apiDesiredCount: number;
  readonly logRetentionDays: logs.RetentionDays;
  readonly deletionProtection: boolean;
  /**
   * Tag of the images in the zentax/<env>/{api,migrate} ECR repositories. A
   * top-level `--context imageTag=<sha>` (what the deploy pipeline passes)
   * wins over environments.<env>.imageTag; default "latest".
   */
  readonly imageTag: string;
  readonly cpuArchitecture: 'X86_64' | 'ARM64';
  /** Optional e-mail subscribed to the environment's alarm topic. */
  readonly alarmEmail?: string;
  /**
   * Whether this environment's Data stack creates the account+region-wide
   * `RDSOSMetrics` log group (enhanced monitoring writes to it). Exactly one
   * environment per account+region may own it; default true.
   */
  readonly ownsRdsOsMetricsLogGroup: boolean;
  /** Explicit AZs (default: <region>a, <region>b — avoids an AWS lookup at synth). */
  readonly availabilityZones: string[];
  /**
   * Optional: skip the Network stack's deploy-time lookup of the CloudFront
   * origin-facing prefix list (the ALB security group's only ingress source).
   */
  readonly cloudFrontPrefixListId?: string;
  /**
   * The api's CORS_ALLOWED_ORIGINS (comma-separated browser origins). The
   * public origin belongs to the UI app (CloudFront / custom domain), so it is
   * configured here rather than referenced; the web tier proxies every
   * browser call and strips `Origin`, so this value only has to satisfy the
   * api's fail-closed validation until a browser talks to the api directly.
   * Default: a reserved `.invalid` placeholder for the environment.
   */
  readonly corsAllowedOrigins: string;
  /** RemovalPolicy for data-bearing resources: RETAIN in production, DESTROY elsewhere. */
  readonly dataRemovalPolicy: cdk.RemovalPolicy;
}

export interface GithubOidcConfig {
  readonly account: string;
}

const ENV_TITLES: Record<EnvName, string> = { staging: 'Staging', production: 'Production' };

function requireString(obj: Record<string, unknown>, key: string, fallback?: string): string {
  const v = obj[key] ?? fallback;
  if (typeof v !== 'string' || v.length === 0) throw new Error(`context: "${key}" must be a non-empty string`);
  return v;
}
function requireNumber(obj: Record<string, unknown>, key: string, fallback?: number): number {
  const v = obj[key] ?? fallback;
  if (typeof v !== 'number' || !Number.isFinite(v)) throw new Error(`context: "${key}" must be a number`);
  return v;
}
function requireBoolean(obj: Record<string, unknown>, key: string, fallback?: boolean): boolean {
  const v = obj[key] ?? fallback;
  if (typeof v !== 'boolean') throw new Error(`context: "${key}" must be a boolean`);
  return v;
}
function optionalString(obj: Record<string, unknown>, key: string): string | undefined {
  const v = obj[key];
  if (v === undefined || v === null || v === '') return undefined;
  if (typeof v !== 'string') throw new Error(`context: "${key}" must be a string`);
  return v;
}

/**
 * Keys that belong to the UI app's config only. They are refused here so a
 * web-tier setting cannot silently linger in this app's cdk.json after the
 * split (it would never take effect).
 */
export const WEB_ONLY_KEYS = [
  'webDesiredCount', 'originVerifyVersion', 'domainName', 'certificateArn', 'hostedZoneId', 'hostedZoneName', 'maxUploadBytes',
] as const;

export function toRetentionDays(days: number): logs.RetentionDays {
  const allowed = Object.values(logs.RetentionDays).filter((v): v is number => typeof v === 'number');
  if (!allowed.includes(days)) {
    throw new Error(`context: logRetentionDays=${days} is not a valid CloudWatch retention (allowed: ${allowed.join(', ')})`);
  }
  return days as logs.RetentionDays;
}

export function isEnvName(s: unknown): s is EnvName {
  return s === 'staging' || s === 'production';
}

/** Read one environment's configuration from the app context. */
export function loadEnvConfig(scope: cdk.App, name: EnvName): EnvConfig {
  const all = (scope.node.tryGetContext('environments') ?? {}) as Record<string, Record<string, unknown>>;
  const raw = all[name];
  if (!raw) throw new Error(`context: environments.${name} is missing from cdk.json`);
  for (const key of WEB_ONLY_KEYS) {
    if (raw[key] !== undefined) {
      throw new Error(`context: environments.${name}.${key} belongs to the UI app (zentax-ui/infra), not here — remove it`);
    }
  }

  const account = requireString(raw, 'account', scope.node.tryGetContext('account') ?? DEFAULT_ACCOUNT);
  const region = requireString(raw, 'region', scope.node.tryGetContext('region') ?? DEFAULT_REGION);
  const azsRaw = raw.availabilityZones;
  const availabilityZones = Array.isArray(azsRaw) && azsRaw.length >= 2
    ? azsRaw.map(String)
    : [`${region}a`, `${region}b`];

  const cpuArch = requireString(raw, 'cpuArchitecture', 'X86_64');
  if (cpuArch !== 'X86_64' && cpuArch !== 'ARM64') throw new Error('context: cpuArchitecture must be X86_64 or ARM64');
  const deletionProtection = requireBoolean(raw, 'deletionProtection', name === 'production');

  // `cdk deploy --context imageTag=<sha>` (the pipeline) beats the per-env default.
  const cliImageTag = scope.node.tryGetContext('imageTag');
  if (cliImageTag !== undefined && (typeof cliImageTag !== 'string' || !/^[\w][\w.-]{0,127}$/.test(cliImageTag))) {
    throw new Error('context: imageTag must be a valid image tag ([A-Za-z0-9_][A-Za-z0-9_.-]{0,127})');
  }
  const imageTag = (cliImageTag as string | undefined) ?? requireString(raw, 'imageTag', 'latest');

  const alarmEmail = optionalString(raw, 'alarmEmail');
  if (alarmEmail && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(alarmEmail)) {
    throw new Error(`context: environments.${name}.alarmEmail is not an e-mail address`);
  }

  const corsAllowedOrigins = optionalString(raw, 'corsAllowedOrigins') ?? `https://zentax-${name}.invalid`;
  for (const origin of corsAllowedOrigins.split(',')) {
    if (!/^https?:\/\/[^\s/,]+$/.test(origin.trim())) {
      throw new Error(`context: environments.${name}.corsAllowedOrigins must be comma-separated origins (scheme://host[:port]), got "${origin}"`);
    }
  }

  return {
    name,
    title: ENV_TITLES[name],
    account,
    region,
    cidr: requireString(raw, 'cidr'),
    dbInstanceClass: requireString(raw, 'dbInstanceClass'),
    dbMultiAz: requireBoolean(raw, 'dbMultiAz'),
    dbBackupDays: requireNumber(raw, 'dbBackupDays'),
    dbAllocatedStorageGb: requireNumber(raw, 'dbAllocatedStorageGb', 20),
    dbMaxAllocatedStorageGb: requireNumber(raw, 'dbMaxAllocatedStorageGb', 100),
    natGateways: requireNumber(raw, 'natGateways'),
    apiDesiredCount: requireNumber(raw, 'apiDesiredCount'),
    logRetentionDays: toRetentionDays(requireNumber(raw, 'logRetentionDays')),
    deletionProtection,
    imageTag,
    cpuArchitecture: cpuArch,
    alarmEmail,
    ownsRdsOsMetricsLogGroup: requireBoolean(raw, 'ownsRdsOsMetricsLogGroup', true),
    availabilityZones,
    cloudFrontPrefixListId: optionalString(raw, 'cloudFrontPrefixListId'),
    corsAllowedOrigins,
    dataRemovalPolicy: name === 'production' ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
  };
}

/**
 * The account-level OIDC stack needs only the account. The two repositories
 * the deploy roles trust are constants in github-oidc-stack.ts (one role per
 * repository and environment), not context.
 */
export function loadGithubOidcConfig(scope: cdk.App): GithubOidcConfig {
  const account = String(scope.node.tryGetContext('account') ?? DEFAULT_ACCOUNT);
  if (scope.node.tryGetContext('githubRepositories') !== undefined) {
    throw new Error('context: githubRepositories is no longer read — the repositories are constants in lib/github-oidc-stack.ts; remove the key');
  }
  return { account };
}

/** Standard tags applied to every stack of an environment. */
export function envTags(cfg: EnvConfig): Record<string, string> {
  return { Project: 'ZenTax', Environment: cfg.name, ManagedBy: 'cdk' };
}
