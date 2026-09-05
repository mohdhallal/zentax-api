/**
 * The CloudFormation export contract between the two ZenTax CDK apps.
 *
 * The API app (github.com/mohdhallal/zentax-api, infra/) owns the shared
 * foundations and EMITS the Network, Data, Cluster and Api exports; the UI app
 * (github.com/mohdhallal/zentax-ui, infra/) IMPORTS them with
 * `Fn.importValue` and emits the Web exports. The account-level OIDC stack
 * (API app) emits the OIDC exports.
 *
 * This file is identical in both repositories (and printed in ADR-0024).
 * Change it in both, or not at all: a renamed export breaks the other app's
 * next deploy, and CloudFormation refuses to change an export another stack
 * imports.
 */

export const NETWORK_EXPORTS = [
  'vpc-id',
  'vpc-cidr',
  /** Comma-separated list, e.g. "eu-central-1a,eu-central-1b". */
  'availability-zones',
  /** Comma-separated subnet IDs, in availability-zone order. */
  'public-subnet-ids',
  /** Comma-separated subnet IDs, in availability-zone order. */
  'private-subnet-ids',
  'alb-sg-id',
  'web-sg-id',
  'api-sg-id',
  'jobs-sg-id',
] as const;

export const DATA_EXPORTS = [
  'alarm-topic-arn',
  'documents-bucket',
  /** ECR repository URI (<account>.dkr.ecr.<region>.amazonaws.com/zentax/<env>/api). */
  'ecr-api',
  'ecr-migrate',
] as const;

export const CLUSTER_EXPORTS = [
  'cluster-name',
  'cluster-arn',
  /** Cloud Map private DNS namespace, e.g. zentax-staging.local. */
  'namespace-name',
  'namespace-id',
  'namespace-arn',
  'migrate-task-family',
  /** Pinned task-definition revision ARN (never the family). */
  'migrate-task-arn',
  'seed-task-family',
  'seed-task-arn',
  'seed-admin-secret-arn',
] as const;

export const API_EXPORTS = [
  'api-service-name',
  /** http://api.zentax-<env>.local:3000 — what the web tier proxies to. */
  'api-internal-url',
  'api-task-arn',
] as const;

export const WEB_EXPORTS = [
  'web-service-name',
  /** https://<distribution>.cloudfront.net */
  'cloudfront-url',
  'cloudfront-id',
  'alb-dns',
  /** ECR repository URI (<account>.dkr.ecr.<region>.amazonaws.com/zentax/<env>/web). */
  'ecr-web',
  'web-task-arn',
] as const;

export type NetworkExportKey = (typeof NETWORK_EXPORTS)[number];
export type DataExportKey = (typeof DATA_EXPORTS)[number];
export type ClusterExportKey = (typeof CLUSTER_EXPORTS)[number];
export type ApiExportKey = (typeof API_EXPORTS)[number];
export type WebExportKey = (typeof WEB_EXPORTS)[number];

/** Every per-environment export name key. */
export type ExportKey = NetworkExportKey | DataExportKey | ClusterExportKey | ApiExportKey | WebExportKey;

export const EXPORT_KEYS: readonly ExportKey[] = [
  ...NETWORK_EXPORTS,
  ...DATA_EXPORTS,
  ...CLUSTER_EXPORTS,
  ...API_EXPORTS,
  ...WEB_EXPORTS,
];

/** The per-environment export name: `zentax-<env>-<key>`. */
export function exportName(env: string, key: ExportKey): string {
  return `zentax-${env}-${key}`;
}

/** Account-level exports of ZenTax-GithubOidc (API app). */
export const OIDC_EXPORTS = {
  cfnExecutionPolicyArn: 'zentax-cfn-execution-policy-arn',
  /** `zentax-deploy-role-<env>-api` / `zentax-deploy-role-<env>-web`. */
  deployRoleArn: (env: string, service: 'api' | 'web'): string => `zentax-deploy-role-${env}-${service}`,
} as const;
