/**
 * The contract as the tests expect it — spelled out independently of
 * lib/exports.ts so a change to the shared file (which must stay identical
 * in both repositories) is caught here rather than silently propagated.
 */
export const NETWORK_EXPORTS = [
  'vpc-id', 'vpc-cidr', 'availability-zones', 'public-subnet-ids', 'private-subnet-ids',
  'alb-sg-id', 'web-sg-id', 'api-sg-id', 'jobs-sg-id',
] as const;
export const DATA_EXPORTS = ['alarm-topic-arn', 'documents-bucket', 'ecr-api', 'ecr-migrate'] as const;
export const CLUSTER_EXPORTS = [
  'cluster-name', 'cluster-arn', 'namespace-name', 'namespace-id', 'namespace-arn',
  'migrate-task-family', 'migrate-task-arn', 'seed-task-family', 'seed-task-arn', 'seed-admin-secret-arn',
] as const;
export const API_EXPORTS = ['api-service-name', 'api-internal-url', 'api-task-arn'] as const;
export const WEB_EXPORTS = ['web-service-name', 'cloudfront-url', 'cloudfront-id', 'alb-dns', 'ecr-web', 'web-task-arn'] as const;

export const ENV_EXPORTS_BY_STACK = {
  Network: NETWORK_EXPORTS,
  Data: DATA_EXPORTS,
  Cluster: CLUSTER_EXPORTS,
  Api: API_EXPORTS,
} as const;

export const OIDC_EXPORT_NAMES = [
  'zentax-cfn-execution-policy-arn',
  'zentax-deploy-role-staging-api', 'zentax-deploy-role-staging-web',
  'zentax-deploy-role-production-api', 'zentax-deploy-role-production-web',
] as const;

export function exportName(env: string, key: string): string {
  return `zentax-${env}-${key}`;
}
