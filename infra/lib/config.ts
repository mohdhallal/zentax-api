import * as cdk from 'aws-cdk-lib';
import * as logs from 'aws-cdk-lib/aws-logs';

/** The two tiers a cell can be. */
export type Tier = 'staging' | 'production';
export const TIERS: readonly Tier[] = ['staging', 'production'];

/**
 * A cell is keyed by tier + region label — `staging-eu`, `production-eu`
 * (`^(staging|production)-[a-z]{2}$`) — and must be declared in cdk.json
 * (`context.environments.<name>`). Every derived name follows it: stack names
 * use the PascalCase title (`ZenTax-StagingEu-Api`), everything else the name
 * itself (`zentax-staging-eu`, `zentax/staging-eu/api`, `/zentax/staging-eu/api`,
 * export `zentax-staging-eu-vpc-id`, deploy role `zentax-deploy-staging-eu-api`,
 * GitHub environment `staging-eu`).
 */
export type EnvName = `${Tier}-${string}`;
export const ENV_NAME_PATTERN = /^(staging|production)-([a-z]{2})$/;

export const DEFAULT_ACCOUNT = '160117555326';
export const DEFAULT_REGION = 'eu-central-1';

/**
 * Per-environment settings, read from `cdk.json` -> context.environments[env].
 * Nested keys cannot be overridden individually on the CLI (edit cdk.json);
 * the one deploy-time knob, `imageTag`, is therefore also read as a top-level
 * context value (`--context imageTag=<sha>`).
 *
 * Only what the foundations and the api service need lives here. The web
 * tier's settings (desired count, origin-verify rotation counter, hosted zone,
 * entry hostnames, upload limit) belong to the UI app's config.
 */
export interface EnvConfig {
  readonly name: EnvName;
  /** `staging` | `production` — what the name starts with. */
  readonly tier: Tier;
  /** Two-letter region label — what the name ends with (`eu`). */
  readonly regionLabel: string;
  /** Stack-name segment: the PascalCase name (StagingEu / ProductionEu). */
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
   * Optional public hostname of the cell (`eu.staging.zentax.software`). The
   * UI app's Edge stack serves it (certificate, alias records, the entry
   * redirect); here it only shapes the api's public-facing config — the CORS
   * default and PUBLIC_BASE_URL. Both apps' cdk.json carry the same value.
   */
  readonly publicHostname?: string;
  /**
   * The api's CORS_ALLOWED_ORIGINS (comma-separated browser origins). Explicit
   * `corsAllowedOrigins` context wins; otherwise `https://<publicHostname>`
   * when a public hostname is configured, else a reserved `.invalid`
   * placeholder for the environment. The web tier proxies every browser call
   * and strips `Origin`, so the value only has to satisfy the api's
   * fail-closed validation until a browser talks to the api directly.
   */
  readonly corsAllowedOrigins: string;
  /** RemovalPolicy for data-bearing resources: RETAIN in production, DESTROY elsewhere. */
  readonly dataRemovalPolicy: cdk.RemovalPolicy;
}

export interface GithubOidcConfig {
  readonly account: string;
  /** Every cell declared in cdk.json: one api + one web deploy role each. */
  readonly environments: EnvName[];
}

/** The account-level ZenTax-Dns stack: `context.dns` in cdk.json. */
export interface DnsConfig {
  readonly account: string;
  /** Route 53 is global; the stack still needs a home region (eu-central-1). */
  readonly region: string;
  /** The apex, e.g. `zentax.software`. */
  readonly zoneName: string;
  /**
   * Google's `google-site-verification=<TOKEN>` token (the value after `=`;
   * the whole record value is accepted too). Absent -> no record + a synth warning.
   */
  readonly googleSiteVerification?: string;
  /**
   * Google Workspace DKIM public key — the `p=` value of the
   * `google._domainkey` TXT record (the whole `v=DKIM1;k=rsa;p=...` value is
   * accepted too). Absent -> no record + a synth warning.
   */
  readonly googleDkimPublicKey?: string;
}

const HOSTNAME_PATTERN = /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

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
 * split (it would never take effect). `certificateArn` no longer exists in
 * either app (the Edge stack creates its certificate); it stays refused.
 */
export const WEB_ONLY_KEYS = [
  'webDesiredCount', 'originVerifyVersion', 'domainName', 'certificateArn', 'hostedZoneId', 'hostedZoneName', 'entryHostnames', 'maxUploadBytes',
] as const;

export function toRetentionDays(days: number): logs.RetentionDays {
  const allowed = Object.values(logs.RetentionDays).filter((v): v is number => typeof v === 'number');
  if (!allowed.includes(days)) {
    throw new Error(`context: logRetentionDays=${days} is not a valid CloudWatch retention (allowed: ${allowed.join(', ')})`);
  }
  return days as logs.RetentionDays;
}

export function isTier(s: unknown): s is Tier {
  return typeof s === 'string' && (TIERS as readonly string[]).includes(s);
}

/** Syntactic check only: `<staging|production>-<two lowercase letters>`. */
export function isEnvNameSyntax(s: unknown): s is EnvName {
  return typeof s === 'string' && ENV_NAME_PATTERN.test(s);
}

/** `staging-eu` -> `StagingEu`: the `ZenTax-<Title>-*` stack-name segment. */
export function envTitle(name: EnvName): string {
  return name.split('-').map((part) => `${part[0].toUpperCase()}${part.slice(1)}`).join('');
}

/**
 * The cells declared in cdk.json (`context.environments`), in file order.
 * Every key must be a valid cell name — a stray `staging` fails synth.
 */
export function configuredEnvNames(scope: cdk.App): EnvName[] {
  const all = scope.node.tryGetContext('environments');
  if (!all || typeof all !== 'object' || Array.isArray(all)) throw new Error('context: environments must be an object keyed by cell name (cdk.json)');
  const names = Object.keys(all as Record<string, unknown>);
  for (const n of names) {
    if (!isEnvNameSyntax(n)) throw new Error(`context: environments.${n}: cell names are <staging|production>-<two-letter region label>, e.g. staging-eu`);
  }
  if (names.length === 0) throw new Error('context: environments declares no cell (cdk.json)');
  return names as EnvName[];
}

/** A well-formed cell name that cdk.json actually declares. */
export function isEnvName(scope: cdk.App, s: unknown): s is EnvName {
  return isEnvNameSyntax(s) && configuredEnvNames(scope).includes(s);
}

/** Read one environment's configuration from the app context. */
export function loadEnvConfig(scope: cdk.App, name: EnvName): EnvConfig {
  if (!isEnvNameSyntax(name)) throw new Error(`context: "${name}" is not a cell name (<staging|production>-<two-letter region label>, e.g. staging-eu)`);
  const all = (scope.node.tryGetContext('environments') ?? {}) as Record<string, Record<string, unknown>>;
  const raw = all[name];
  if (!raw) throw new Error(`context: environments.${name} is missing from cdk.json`);
  for (const key of WEB_ONLY_KEYS) {
    if (raw[key] !== undefined) {
      throw new Error(`context: environments.${name}.${key} belongs to the UI app (zentax-ui/infra), not here — remove it`);
    }
  }

  // The name is the key; tier + regionLabel are declared explicitly and must agree with it.
  const tier = requireString(raw, 'tier');
  if (!isTier(tier)) throw new Error(`context: environments.${name}.tier must be "staging" or "production", got "${tier}"`);
  const regionLabel = requireString(raw, 'regionLabel');
  if (!/^[a-z]{2}$/.test(regionLabel)) throw new Error(`context: environments.${name}.regionLabel must be two lowercase letters, got "${regionLabel}"`);
  if (name !== `${tier}-${regionLabel}`) {
    throw new Error(`context: environments.${name} declares tier="${tier}" and regionLabel="${regionLabel}" — the key must be "${tier}-${regionLabel}"`);
  }

  const account = requireString(raw, 'account', scope.node.tryGetContext('account') ?? DEFAULT_ACCOUNT);
  const region = requireString(raw, 'region', scope.node.tryGetContext('region') ?? DEFAULT_REGION);
  const azsRaw = raw.availabilityZones;
  const availabilityZones = Array.isArray(azsRaw) && azsRaw.length >= 2
    ? azsRaw.map(String)
    : [`${region}a`, `${region}b`];

  const cpuArch = requireString(raw, 'cpuArchitecture', 'X86_64');
  if (cpuArch !== 'X86_64' && cpuArch !== 'ARM64') throw new Error('context: cpuArchitecture must be X86_64 or ARM64');
  const production = tier === 'production';
  const deletionProtection = requireBoolean(raw, 'deletionProtection', production);

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

  const publicHostname = optionalString(raw, 'publicHostname');
  if (publicHostname && !HOSTNAME_PATTERN.test(publicHostname)) {
    throw new Error(`context: environments.${name}.publicHostname must be a lowercase DNS hostname (e.g. eu.staging.zentax.software), got "${publicHostname}"`);
  }

  const corsAllowedOrigins = optionalString(raw, 'corsAllowedOrigins')
    ?? (publicHostname ? `https://${publicHostname}` : `https://zentax-${name}.invalid`);
  for (const origin of corsAllowedOrigins.split(',')) {
    if (!/^https?:\/\/[^\s/,]+$/.test(origin.trim())) {
      throw new Error(`context: environments.${name}.corsAllowedOrigins must be comma-separated origins (scheme://host[:port]), got "${origin}"`);
    }
  }

  return {
    name,
    tier,
    regionLabel,
    title: envTitle(name),
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
    publicHostname,
    corsAllowedOrigins,
    dataRemovalPolicy: production ? cdk.RemovalPolicy.RETAIN : cdk.RemovalPolicy.DESTROY,
  };
}

/**
 * The account-level OIDC stack needs the account and the list of cells (one
 * api + one web deploy role each). The two repositories the roles trust are
 * constants in github-oidc-stack.ts, not context.
 */
export function loadGithubOidcConfig(scope: cdk.App): GithubOidcConfig {
  const account = String(scope.node.tryGetContext('account') ?? DEFAULT_ACCOUNT);
  if (scope.node.tryGetContext('githubRepositories') !== undefined) {
    throw new Error('context: githubRepositories is no longer read — the repositories are constants in lib/github-oidc-stack.ts; remove the key');
  }
  return { account, environments: configuredEnvNames(scope) };
}

/**
 * `context.dns` for the account-level ZenTax-Dns stack. `zoneName` is required;
 * the two Google values are optional (empty -> the record is omitted and the
 * stack warns). Both accept either the bare value or the whole record value as
 * shown in the Squarespace panel, so a copy-paste of either form works.
 */
export function loadDnsConfig(scope: cdk.App): DnsConfig {
  const raw = scope.node.tryGetContext('dns');
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error('context: "dns" must be an object { zoneName, googleSiteVerification, googleDkimPublicKey } (cdk.json)');
  }
  const obj = raw as Record<string, unknown>;
  const zoneName = requireString(obj, 'zoneName').replace(/\.$/, '');
  if (!HOSTNAME_PATTERN.test(zoneName)) throw new Error(`context: dns.zoneName must be a DNS name (e.g. zentax.software), got "${zoneName}"`);

  let googleSiteVerification = optionalString(obj, 'googleSiteVerification')?.trim();
  if (googleSiteVerification) {
    googleSiteVerification = googleSiteVerification.replace(/^google-site-verification=/, '');
    if (!/^[A-Za-z0-9_-]{8,}$/.test(googleSiteVerification)) {
      throw new Error('context: dns.googleSiteVerification must be the token of the google-site-verification=<TOKEN> TXT record');
    }
  }
  let googleDkimPublicKey = optionalString(obj, 'googleDkimPublicKey')?.replace(/\s+/g, '');
  if (googleDkimPublicKey) {
    googleDkimPublicKey = googleDkimPublicKey.replace(/^v=DKIM1;(?:[a-z]=[^;]*;)*p=/, '');
    if (!/^[A-Za-z0-9+/]{64,}={0,2}$/.test(googleDkimPublicKey)) {
      throw new Error('context: dns.googleDkimPublicKey must be the base64 public key (the p= value) of the google._domainkey TXT record');
    }
  }
  return {
    account: String(scope.node.tryGetContext('account') ?? DEFAULT_ACCOUNT),
    region: String(scope.node.tryGetContext('region') ?? DEFAULT_REGION),
    zoneName,
    googleSiteVerification,
    googleDkimPublicKey,
  };
}

/** Standard tags applied to every stack of an environment. */
export function envTags(cfg: EnvConfig): Record<string, string> {
  return { Project: 'ZenTax', Environment: cfg.name, Tier: cfg.tier, RegionLabel: cfg.regionLabel, ManagedBy: 'cdk' };
}
