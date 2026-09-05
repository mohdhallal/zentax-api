# ZenTax API infrastructure (AWS CDK v2, TypeScript)

Infrastructure-as-code for the **API half** of the ZenTax SaaS *cell* (ADR-0005,
ADR-0024): the shared foundations every cell is built on, the Go api service,
the account-level GitHub Actions federation (ADR-0016) and the account-level
DNS zone of the product domain. Everything here is in `infra/` and has its own
`package.json`; the Go module is untouched.

The web half — the web service, the ALB, CloudFront, the custom domain
(certificate, alias records, entry redirect) and the CloudFront alarm — is the
CDK app of the UI repository (`github.com/mohdhallal/zentax-ui`, `infra/`).
The two apps never reference each other's constructs: the UI app reads this
app's stacks **only through the CloudFormation export contract** below
(`lib/exports.ts`, identical in both repositories) and one pasted value (the
hosted zone ID, see "ZenTax-Dns").

```
infra/
  bin/zentax.ts            CDK app entry: --context env=staging-eu|production-eu [--context imageTag=<sha>]
  cdk.json                 per-cell settings (context.environments.*), context.dns, feature flags; "// ..." keys are comments
  lib/config.ts            typed loader/validator for that context (cell names, tier/regionLabel, publicHostname, dns; refuses the UI app's keys)
  lib/exports.ts           THE EXPORT CONTRACT — same file as zentax-ui/infra/lib/exports.ts, byte for byte
  lib/network-stack.ts     ZenTax-<Title>-Network  VPC, endpoints, ALL security groups (incl. alb + web), flow-log group
  lib/data-stack.ts        ZenTax-<Title>-Data     KMS, RDS Postgres 16, secrets, documents bucket, api + migrate ECR, log groups, alarm topic + RDS alarms
  lib/cluster-stack.ts     ZenTax-<Title>-Cluster  ECS cluster + Cloud Map namespace, migrate + seed task definitions
  lib/api-stack.ts         ZenTax-<Title>-Api      Fargate api service (Cloud Map "api"), its roles + grants, running-task alarm
  lib/alarms.ts            alarm topic / wiring helpers, RDS max_connections table
  lib/ecs-roles.ts         task/execution role naming shared by Cluster and Api
  lib/github-oidc-stack.ts ZenTax-GithubOidc       GitHub OIDC provider, api + web deploy roles per cell, ZenTaxCfnExecutionPolicy (+ …Edge)
  lib/dns-stack.ts         ZenTax-Dns              Route 53 hosted zone for zentax.software + the Squarespace / Google Workspace records, SPF, DMARC
  test/                    jest + aws-cdk-lib/assertions (both cells, OIDC, Dns, the contract, the split rules)
```

```bash
cd infra
npm ci
npm test                                   # 133 tests over staging-eu + production-eu + OIDC + Dns + the export contract
npx cdk synth --context env=staging-eu     # no AWS calls: account/region/AZs are explicit
npx cdk synth --context env=production-eu
npx cdk synth --context env=staging-eu --context imageTag=$(git rev-parse --short=12 HEAD)   # what the pipeline does
npx cdk ls                                 # omit env to list every stack
```

## Cells: tier + region label

A cell is one deployable copy of ZenTax. Its name is `<tier>-<regionLabel>`
— `staging-eu`, `production-eu` (pattern `^(staging|production)-[a-z]{2}$`) —
and it must be declared in `cdk.json` under `context.environments`, with
`tier` and `regionLabel` spelled out explicitly (the loader refuses a key that
does not equal `tier-regionLabel`). Nothing is deployed yet, so this naming is
the naming; a second region later is just `production-us`.

Every derived name follows from the cell name:

| | staging-eu | production-eu |
|---|---|---|
| Stack names (`ZenTax-<Title>-*`, Title = PascalCase) | `ZenTax-StagingEu-Network/-Data/-Cluster/-Api` (+ UI: `-Web`, `-Edge`) | `ZenTax-ProductionEu-…` |
| Cluster, VPC, RDS identifier, security groups | `zentax-staging-eu`, `zentax-staging-eu-{alb,web,api,jobs,db}` | `zentax-production-eu…` |
| Cloud Map namespace | `zentax-staging-eu.local` (api at `api.zentax-staging-eu.local:3000`) | `zentax-production-eu.local` |
| ECR repositories | `zentax/staging-eu/{api,migrate}` (+ UI: `/web`) | `zentax/production-eu/…` |
| Log groups | `/zentax/staging-eu/{api,migrate,seed,vpc-flow-logs,…}` | `/zentax/production-eu/…` |
| Secrets, KMS alias | `zentax/staging-eu/{db-master,app-db,auth-encryption-key,seed-admin}`, `alias/zentax/staging-eu/data` | `zentax/production-eu/…` |
| Documents bucket | `zentax-staging-eu-documents-<account>-<region>` | `zentax-production-eu-documents-…` |
| Task / execution roles, task families | `zentax-staging-eu-{api,migrate,seed}-{task,exec}`, `zentax-staging-eu-{api,migrate,seed}` | `zentax-production-eu-…` |
| CloudFormation exports | `zentax-staging-eu-<key>` | `zentax-production-eu-<key>` |
| Deploy roles / GitHub environments | `zentax-deploy-staging-eu-{api,web}`, GitHub environment **`staging-eu`** in both repositories | `zentax-deploy-production-eu-{api,web}`, **`production-eu`** |
| Tags on every stack | `Environment=staging-eu`, `Tier=staging`, `RegionLabel=eu` (+ `Project=ZenTax`, `ManagedBy=cdk`) | `Environment=production-eu`, `Tier=production`, `RegionLabel=eu` |
| Public hostname (`publicHostname`, served by the UI app's Edge stack) | `eu.staging.zentax.software` | `eu.app.zentax.software` (+ entry `app.zentax.software` → 301) |

The pipelines derive the stack-name segment with `.github/workflows/scripts/env-title.sh`
(`staging-eu` → `StagingEu`), the CDK app with `envTitle()` in `lib/config.ts`.
Data-policy decisions (`RETAIN`/`DESTROY`, deletion protection, snapshot on
delete) key off the **tier**, never the name.

## Which repository owns what

| | API repository (this app) | UI repository (`zentax-ui/infra`) |
|---|---|---|
| Stacks | `ZenTax-GithubOidc`, `ZenTax-Dns` (account-level, admin-deployed); per cell `ZenTax-<Title>-Network`, `-Data`, `-Cluster`, `-Api` | per cell `ZenTax-<Title>-Web` (eu-central-1), `-Edge` (us-east-1) |
| Images | `zentax/<cell>/api`, `zentax/<cell>/migrate` (ECR in the Data stack) | `zentax/<cell>/web` (ECR in the Web stack) |
| Services / tasks | api service; migrate + seed one-off tasks | web service |
| Front door | — | ALB (+ access-log bucket), origin-verify secret, CloudFront; with the custom domain on: the ACM certificate (DNS-validated into the ZenTax-Dns zone), the A/AAAA alias records for the public hostname and the entry hostnames, the `app.zentax.software` → `eu.app.zentax.software` redirect function, HSTS |
| DNS | the `zentax.software` hosted zone and its non-cell records (`ZenTax-Dns`) | writes the cells' records into that zone by ID |
| Security groups | **all** of them (alb, web, api, jobs, db) — see "Stack layout and why" | imports `alb-sg-id` / `web-sg-id` |
| Alarms | RDS (Data), api running tasks (Api); the home-region topic | web running tasks, ALB 5xx / unhealthy hosts (Web, on the imported topic); CloudFront 5xx + the us-east-1 topic (Edge) |
| Deploy role | `zentax-deploy-<cell>-api` (trusts `repo:mohdhallal/zentax-api:environment:<cell>`) | `zentax-deploy-<cell>-web` (trusts `repo:mohdhallal/zentax-ui:environment:<cell>`) — created **here**, in `ZenTax-GithubOidc` |
| Pipeline | `.github/workflows/deploy.yml`: images → Network+Data+Cluster → migrate → Api → services-stable (→ seed) | images → Web+Edge → services-stable → smoke through CloudFront |

Deploy order on a fresh cell: `ZenTax-Dns` and `ZenTax-GithubOidc` (admin, once
per account), then this app's cell stacks (they create every export the Web
stack imports), then the UI pipeline. Afterwards the two pipelines are
independent: an api release never touches the web tier (the web finds the api
by its Cloud Map name), a web release never touches the api.

## The export contract (`lib/exports.ts`)

Every value the UI app needs is a CloudFormation export named
`zentax-<cell>-<key>` (account-level OIDC exports aside). The UI app consumes
them with `cdk.Fn.importValue(exportName(env, key))` and the `from*Attributes`
/ `from*Id` / `fromTopicArn` factories — no context lookups, no
`Fn::GetStackOutput` into these stacks. CloudFormation refuses to delete or
change an export another stack imports, which is the safety net: a rename is
"add the new name, move the importers, remove the old name", never an in-place
edit. Change the file in both repositories in the same change (a test here
compares it with `../zentax-ui/infra/lib/exports.ts` when that checkout is
present). `exportName()` is unchanged by the cell naming: the cell name simply
is the `<env>` segment.

| Stack (emitter) | Key | Value |
|---|---|---|
| `ZenTax-<Title>-Network` (API) | `vpc-id` | VPC ID |
| | `vpc-cidr` | VPC CIDR block |
| | `availability-zones` | comma-separated list, e.g. `eu-central-1a,eu-central-1b` |
| | `public-subnet-ids` | comma-separated subnet IDs, in availability-zone order (the ALB) |
| | `private-subnet-ids` | comma-separated subnet IDs, in availability-zone order (services, `ecs run-task`) |
| | `alb-sg-id` | security group of the public ALB (CloudFront prefix list :80 only) |
| | `web-sg-id` | security group of the web tasks (from alb :5000) |
| | `api-sg-id` | security group of the api tasks (from web :3000) |
| | `jobs-sg-id` | security group of the migrate/seed tasks (no inbound) |
| `ZenTax-<Title>-Data` (API) | `alarm-topic-arn` | the cell's SNS alarm topic (home region) |
| | `documents-bucket` | documents bucket name |
| | `ecr-api` | repository URI `<account>.dkr.ecr.<region>.amazonaws.com/zentax/<cell>/api` |
| | `ecr-migrate` | repository URI of `zentax/<cell>/migrate` |
| `ZenTax-<Title>-Cluster` (API) | `cluster-name` | ECS cluster name `zentax-<cell>` |
| | `cluster-arn` | ECS cluster ARN |
| | `namespace-name` | Cloud Map private DNS namespace, `zentax-<cell>.local` |
| | `namespace-id` | Cloud Map namespace ID |
| | `namespace-arn` | Cloud Map namespace ARN |
| | `migrate-task-family` | `zentax-<cell>-migrate` |
| | `migrate-task-arn` | pinned task-definition revision ARN (never the family) |
| | `seed-task-family` | `zentax-<cell>-seed` |
| | `seed-task-arn` | pinned task-definition revision ARN |
| | `seed-admin-secret-arn` | `zentax/<cell>/seed-admin` (the first admin's initial password) |
| `ZenTax-<Title>-Api` (API) | `api-service-name` | `zentax-<cell>-api` |
| | `api-internal-url` | `http://api.zentax-<cell>.local:3000` — what the web tier proxies to (`GO_API_URL`) |
| | `api-task-arn` | pinned task-definition revision ARN |
| `ZenTax-<Title>-Web` (UI) | `web-service-name` | `zentax-<cell>-web` |
| | `alb-dns` | ALB DNS name (the CloudFront origin) |
| | `ecr-web` | repository URI of `zentax/<cell>/web` |
| | `web-task-arn` | pinned task-definition revision ARN |
| `ZenTax-<Title>-Edge` (UI, **us-east-1**) | `cloudfront-url` | `https://<distribution>.cloudfront.net` — exported in us-east-1; nothing imports it (the same `exports.ts` names it) |
| | `cloudfront-id` | distribution ID (us-east-1, same) |
| `ZenTax-GithubOidc` (API, account-level) | `zentax-cfn-execution-policy-arn` | `ZenTaxCfnExecutionPolicy` — pass to `cdk bootstrap --cloudformation-execution-policies` |
| | `zentax-deploy-role-<cell>-api` | role ARN for the API repository's GitHub environment `<cell>` |
| | `zentax-deploy-role-<cell>-web` | role ARN for the UI repository's GitHub environment `<cell>` |

`ZenTax-GithubOidc` also exports `zentax-cfn-execution-policy-edge-arn`
(`ZenTaxCfnExecutionPolicyEdge`, the second bootstrap policy). It is
deliberately *not* in `lib/exports.ts`: no stack imports it, only the
bootstrap command below reads it. `ZenTax-Dns` has plain outputs
(`HostedZoneId`, `NameServers`), no exports: the UI app takes the zone ID from
its own `cdk.json`, never from an import.

The pipelines read the same values through the CloudFormation **output keys**
(`scripts/stack-output.sh`, by stack name): `ClusterName`, `ApiServiceName`,
`MigrateTaskDefinitionArn`, `SeedTaskDefinitionArn`, `SeedAdminSecretArn`,
`PrivateSubnetIds`, `JobsSecurityGroupId`, `ApiRepositoryUri`,
`MigrateRepositoryUri`.

## Stack layout and why

Four stacks per cell here, `Network -> Data -> Cluster -> Api`, then the UI
app's `Web -> Edge` on top; and two account-level stacks that are deployed
once, by hand:

- **Network** owns every security group and the rules between them — including
  the ALB's and the web tasks' groups, although those resources are created by
  the other app. RDS (Data) must admit the api tasks (Api) while being created
  first, and the web group must admit the ALB's group, which belongs to a stack
  of another app: the groups cannot live next to their consumers without a
  dependency cycle, and a group defined in one app cannot reference a group
  defined in the other at synth time. The Web stack imports `alb-sg-id` and
  `web-sg-id` and attaches them.
- **Data** is everything stateful or expensive to recreate, plus the cell's
  alarm topic and the RDS alarms (they live next to the instance). The web
  tier's state — its ECR repository, its log group and the origin-verify
  secret CloudFront and the ALB share — lives with the ALB, in the Web stack.
- **Cluster** holds the ECS cluster, its Cloud Map namespace, and the `migrate`
  and `seed` task definitions. The api cannot boot until the first migration
  has created the `zentax_app` role it connects as — creating Api first would
  crash-loop the api into a circuit-breaker rollback and fail the stack. With
  the cluster and the jobs in their own stack, migrations run before Api
  exists; that is also the pipeline order (migrate, then roll the service). The
  Web stack places its service on this cluster via the cluster + namespace
  exports.
- **Api** is the stateless part that changes on every api release: the service,
  its task/execution roles and grants, its Cloud Map record, its alarm. It has
  no public surface at all.
- **Dns** (account-level) is the `zentax.software` hosted zone with the
  records that are *not* a cell's: the Squarespace marketing site and Google
  Workspace mail. It is admin-deployed so that creating a hosted zone is never
  something a pipeline can do; the cells' own records are written into it by
  the UI app's Edge stack (see "ZenTax-Dns" below).
- **GithubOidc** (account-level) is the CI trust root: the OIDC provider, the
  deploy roles, and the two execution policies the bootstrap uses.

Cross-stack references *inside* this app use CloudFormation's weak
`Fn::GetStackOutput` (the default `@aws-cdk/core:defaultCrossStackReferences:
"weak"` for new CDK apps), so a producer stack can change a value without the
consumer blocking the update. References *between* the apps are the exports
above — strong by nature, which is what protects the contract.

A second account per cell is a context change: set
`environments.<cell>.account` (and `region`) in both apps, bootstrap that
account, and deploy that account's own `ZenTax-GithubOidc`.

## What gets created

Per cell (`eu-central-1`, account `160117555326` by default). The **Owner**
column says which app creates the line; the UI app's lines are listed for
completeness so this table stays the one picture of the cell.

| Component | Owner | staging-eu | production-eu |
|---|---|---|---|
| VPC | API | `zentax-staging-eu`, `10.20.0.0/16`, 2 AZs, 2 public /24 + 2 private /22, **1 NAT** | `zentax-production-eu`, `10.30.0.0/16`, same layout, **2 NATs** |
| VPC endpoints | API | S3 gateway; interface: ECR api, ECR dkr, Secrets Manager, CloudWatch Logs | same |
| Security groups | API | `alb` (CloudFront origin-facing prefix list :80 only), `web` (from alb :5000), `api` (from web :3000), `jobs` (no inbound), `db` (from api + jobs :5432) | same |
| RDS PostgreSQL 16 | API | `zentax-staging-eu`: `db.t4g.small`, single-AZ, 20 GB gp3 (autoscale to 100), 7-day PITR, **no** deletion protection, `DESTROY` on stack delete, automated backups deleted with it | `zentax-production-eu`: `db.t4g.medium`, **Multi-AZ**, 50 GB gp3 (to 200), **35-day PITR**, deletion protection, **`SNAPSHOT`** on delete (never a plain delete), automated backups kept |
| RDS extras (both) | API | CMK-encrypted storage, parameter group `rds.force_ssl=1`, slow-statement + connection logging with `log_parameter_max_length(_on_error)=0` (bind values never reach the log export), Performance Insights (7 days, CMK), enhanced monitoring 60 s (`RDSOSMetrics` log group pre-created with the retention by the owning cell — see cdk.json `ownsRdsOsMetricsLogGroup`), `postgresql` log export to `/aws/rds/instance/zentax-<cell>/postgresql`, RDS CA `rds-ca-rsa2048-g1` | |
| KMS | API | `alias/zentax/<cell>/data`, rotation on | same, `RETAIN` |
| Secrets Manager | API | `zentax/<cell>/db-master` (generated by RDS), `zentax/<cell>/app-db` `{username: zentax_app, password}`, `zentax/<cell>/auth-encryption-key` (64 chars, no punctuation), `zentax/<cell>/seed-admin` (32 chars, the first admin's initial password) | same, `RETAIN` |
| Secrets Manager | UI | `zentax/<cell>/origin-verify` (the CloudFront → ALB shared header value): primary in the home region, **replicated to us-east-1** so the Edge stack's CloudFront origin resolves it by name in its own region | same |
| S3 | API | `zentax-<cell>-documents-<acct>-<region>`: SSE-KMS (CMK, bucket key), versioned, block-all-public, TLS-only policy, non-current versions expire after **`dbBackupDays` (7 d)** — one retention window with the database (ADR-0007/0014), incomplete multipart aborted 7 d | same, non-current versions **35 d**, `RETAIN` |
| S3 | UI | `zentax-<cell>-alb-logs-…` (ALB access logs, expire with log retention) | same |
| ECR | API | `zentax/<cell>/api`, `zentax/<cell>/migrate`: scan on push, keep last 30 tagged, untagged expire 7 d | same, `RETAIN` |
| ECR | UI | `zentax/<cell>/web`, same rules | same |
| CloudWatch Logs | API | `/zentax/<cell>/{api,migrate,seed,vpc-flow-logs,cdk/cloudfront-prefix-list-lookup}`, `/aws/rds/instance/zentax-<cell>/postgresql`, `RDSOSMetrics` (owner only: staging-eu) — every group explicit, **30 days** | **365 days** |
| CloudWatch Logs | UI | `/zentax/<cell>/web` | |
| ECS (Cluster stack) | API | cluster `zentax-<cell>`, Container Insights, Cloud Map namespace `zentax-<cell>.local` | same |
| api service | API | 0.5 vCPU / 1 GB, **1 task**, autoscale 1–2 on CPU 60 %, circuit breaker + rollback, Cloud Map `api.zentax-<cell>.local`; `PUBLIC_BASE_URL=https://eu.staging.zentax.software` | **2 tasks**, autoscale 2–4; `PUBLIC_BASE_URL=https://eu.app.zentax.software` |
| web service | UI | 0.25 vCPU / 0.5 GB, **1 task**, autoscale 1–2 | **2 tasks**, autoscale 2–4 |
| One-off jobs (Cluster stack) | API | `zentax-<cell>-migrate` (migrate image) and `zentax-<cell>-seed` (api image, entrypoint `seed-admin`, `SEED_ADMIN_PASSWORD` from the seed-admin secret) task definitions, 0.25 vCPU / 0.5 GB; families and pinned-revision ARNs exported | same |
| ALB (Web) + CloudFront (Edge, us-east-1) | UI | Web stack: internet-facing HTTP:80 ALB (default 403, one rule on `X-Origin-Verify-v<n>`). Edge stack: the CloudFront distribution and, with the custom domain on, the ACM certificate for `eu.staging.zentax.software`, A/AAAA aliases in the ZenTax-Dns zone, HSTS, the CloudFront 5xx alarm (see the UI app's README) | same for `eu.app.zentax.software` + the `app.zentax.software` entry hostname (alias + 301 redirect to the public hostname) |
| IAM | API | task/execution roles `zentax-<cell>-{api,migrate,seed}-{task,exec}`; api task role: `s3:GetObject/PutObject/DeleteObject/AbortMultipartUpload` on the documents bucket's objects + `s3:ListBucket` on the bucket (no `*Version` action: versioning is the undo log) + encrypt/decrypt on the CMK, nothing else | same |
| IAM | UI | `zentax-<cell>-web-{task,exec}` | same |
| VPC flow logs | API | REJECT traffic to `/zentax/<cell>/vpc-flow-logs` | same |
| Alarms (SNS `zentax-<cell>-alarms`, optional e-mail via context `alarmEmail`) | API | RDS: CPU > 80 % (5 min), free storage < 10 % of the allocated GB, connections > 80 % of the class's default `max_connections` (`LEAST(RAM/9531392, 5000)`: 225 for t4g.small), freeable memory < 10 % of the class RAM; ECS: api `RunningTaskCount` < desired for 5 min (Container Insights); all notify on ALARM and OK | same (450 connections for t4g.medium) |
| Alarms | UI | web `RunningTaskCount`, ALB `HTTPCode_ELB_5XX_Count` > 10 / 5 min, `UnHealthyHostCount` > 0 (on the imported topic); Edge (us-east-1): CloudFront `5xxErrorRate` > 5 % + its own topic | same |

### Account-level: `ZenTax-GithubOidc`

Region-agnostic, deployed with the admin's own credentials — see "First
deployment": the `token.actions.githubusercontent.com` OIDC provider, **two
deploy roles per cell** (four today) and the managed policies
`ZenTaxCfnExecutionPolicy` + `ZenTaxCfnExecutionPolicyEdge`. The cells come
from `cdk.json` (`context.environments`): declaring a cell there creates its
roles.

| Role | Trusts | May |
|---|---|---|
| `zentax-deploy-<cell>-api` | `repo:mohdhallal/zentax-api:environment:<cell>` | ECR login + push to `zentax/<cell>/{api,migrate}`; `ecs:RunTask` on the `zentax-<cell>-migrate` / `-seed` task definitions in cluster `zentax-<cell>`; describe/stop tasks; register/describe task definitions (the pin script); update/describe the **api** service; `iam:PassRole` on `zentax-<cell>-{api,migrate,seed}-*` to ECS only; read the api/migrate/seed logs |
| `zentax-deploy-<cell>-web` | `repo:mohdhallal/zentax-ui:environment:<cell>` | ECR login + push to `zentax/<cell>/web`; update/describe the **web** service; `iam:PassRole` on `zentax-<cell>-web-*` to ECS only; read the web logs |
| both | | `sts:AssumeRole` on the four `cdk-hnb659fds-*` bootstrap roles (the CDK deploy path), the bootstrap version parameter, and read-only `cloudformation:DescribeStacks`/`ListStacks` (on `*`: the CLI and the workflow scripts call them without a stack name) |

`Resource: "*"` appears only for actions that have no resource-level scope. The
repositories are constants in `github-oidc-stack.ts` (`DEPLOY_REPOSITORIES`),
not context: a role is one repository × one cell × one half of the cell.

`ZenTaxCfnExecutionPolicy` is what CloudFormation itself may do while deploying
**any** ZenTax stack — this app's and the UI app's, since both run through the
same bootstrap; it replaces the bootstrap's default `AdministratorAccess`.
EC2 is limited to the network plane (VPC/subnets/routing/gateways/security
groups/endpoints/flow logs — no instances or volumes); S3, ECR, Secrets
Manager, Logs, SNS, CloudWatch, Lambda, SSM, ELB and RDS are scoped to the
ZenTax names (`zentax-*`, `zentax/*`, `/zentax/*`, plus `ZenTax-*` for
CloudFormation-generated names — the cell names `zentax-staging-eu…` fall
under the same globs; Secrets Manager is `secretsmanager:*` on
`secret:zentax/*` in **every** region, which already includes
`ReplicateSecretToRegions` / `RemoveRegionsFromReplication` for the Web
stack's us-east-1 replica of `zentax/<cell>/origin-verify` — nothing to add
for it, a test pins that); Route 53 record changes on `hostedzone/*` (the Edge
stack's alias and certificate-validation records); IAM role management and
`PassRole` only for role names `zentax-*`, `ZenTax-*` and `cdk-*`, and no
managed-policy, user, group or OIDC-provider action at all (so the policy can
never widen itself). ECS, Service Discovery, Application Auto Scaling,
CloudFront and KMS are service-wide because their physical names are
generated. It fits IAM's 6144-character cap (a test checks) — just: it is at
6121 characters, which is why the ACM permissions the Edge stack's
custom-domain certificate needs live in a **second** managed policy,
`ZenTaxCfnExecutionPolicyEdge` (`acm:RequestCertificate`, `DescribeCertificate`,
`DeleteCertificate`, `AddTagsToCertificate`, `RemoveTagsFromCertificate`,
`ListTagsForCertificate` on `*` — certificate ARNs are generated; no
import/export/renew). The first policy carries no `acm:` action at all. Both
ARNs go to the bootstrap, comma-separated (step 2 below); an account
bootstrapped with only the first is fixed by re-running the bootstrap with
both — it updates the execution role in place. Hosted-zone **creation** is in
neither policy: `ZenTax-Dns` is
admin-deployed. A missing permission shows up as an `AccessDenied` in the
stack events: extend the statement in `github-oidc-stack.ts` and redeploy
`ZenTax-GithubOidc` — the ARNs stay.

### Account-level: `ZenTax-Dns`

The public hosted zone for **`zentax.software`** (`context.dns.zoneName`) and
every record it carries today, as code. The registrar stays Squarespace; only
the authoritative DNS moves to Route 53. Deployed by the admin identity with
the CLI's own credentials (`CliCredentialsStackSynthesizer`, like the OIDC
stack) in eu-central-1 (Route 53 itself is global); the zone is
`RemovalPolicy.RETAIN` (deleting the stack never deletes the zone — a recreated
zone would get different name servers) and tagged `Project=ZenTax`.

| Record | Value | Source |
|---|---|---|
| `A @` | `198.185.159.144`, `198.185.159.145`, `198.49.23.144`, `198.49.23.145` (one record set) | Squarespace site — panel snapshot 2026-09-05, hard-coded in `dns-stack.ts` |
| `CNAME www` | `ext-sq.squarespace.com` | Squarespace site — same |
| `MX @` | `1 aspmx.l.google.com`, `5 alt1.aspmx.l.google.com`, `5 alt2.aspmx.l.google.com`, `10 alt3.aspmx.l.google.com`, `10 alt4.aspmx.l.google.com` | Google Workspace — same |
| `TXT @` | `google-site-verification=<TOKEN>` **and** `v=spf1 include:_spf.google.com ~all` | token from `context.dns.googleSiteVerification`; SPF is new (missing at Squarespace). Route 53 allows one TXT record set per name, so both values share it |
| `TXT google._domainkey` | `v=DKIM1;k=rsa;p=<KEY>` | key from `context.dns.googleDkimPublicKey`; longer than 255 characters, emitted as 255-character quoted chunks (a test proves it on the synthesized template) |
| `TXT _dmarc` | `v=DMARC1; p=none; rua=mailto:dmarc@zentax.software` | new (missing at Squarespace); monitor-only |

Not recreated on purpose: Squarespace's HTTPS/SVCB record and the
`_domainconnect` CNAME (they serve Squarespace's own DNS, not the site).

**The two Google values** are pasted in `infra/cdk.json` (2026-09-05, the
untruncated values from the Squarespace panel — click each record there to see
it): `context.dns.googleSiteVerification` (the token after
`google-site-verification=`; the whole value is accepted too) and
`context.dns.googleDkimPublicKey` (the `p=` value of `google._domainkey`; the
whole `v=DKIM1;k=rsa;p=…` value is accepted too, wrapped whitespace is
dropped). Neither value is a secret (both are published in DNS); a test pins
that the committed ones are non-empty and well-formed. Should either be
emptied (a DKIM key rotation, say) its record is **omitted** and `cdk synth`
prints a warning — CI stays green, but never delegate or deploy in that state.
The stack is termination-protected and the zone **and every record set** are
`RETAIN`ed, so a stack delete never empties the delegated zone.

Outputs: `HostedZoneId` — paste into `zentax-ui/infra/cdk.json`
(`environments.<cell>.hostedZoneId`; the custom domain is on only when that
and `publicHostname` are both set) — and `NameServers` (the four NS, joined by
`, `) — enter them at Squarespace (Domains → DNS → nameservers). The cells'
own hostnames (`eu.staging.zentax.software`, `eu.app.zentax.software`,
`app.zentax.software`) are **not** here: the certificate, the alias records
and the entry redirect live in the UI app's Edge stack, which writes into this
zone by ID.

### Environment contract (what the containers receive)

| Container | Owner | Environment | Secrets (`ValueFrom`, never a value in the template) |
|---|---|---|---|
| api | API | `APP_ENV=<cell>`, `DB_HOST`, `DB_PORT`, `DB_NAME=zentax`, `DB_USER=zentax_app`, `DB_SSLMODE=require`, `CORS_ALLOWED_ORIGINS` (see below), `STORAGE_DRIVER=s3`, `STORAGE_S3_BUCKET`, `STORAGE_S3_REGION`, `LOG_FORMAT=json`, `PUBLIC_BASE_URL=https://<publicHostname>` (the Go config's `app.publicBaseUrl`: the base for the absolute links the api will hand out — no consumer yet, the invite link is built by the UI from its own origin until e-mail delivery lands; omitted when no public hostname is configured) | `DB_PASSWORD` ← app-db `.password`, `AUTH_ENCRYPTION_KEY` ← auth-encryption-key |
| migrate | API | `PGHOST`, `PGPORT`, `PGDATABASE=zentax`, `PGSSLMODE=require`, `APP_DB_USER=zentax_app` | `PGUSER`/`PGPASSWORD` ← db-master, `APP_DB_PASSWORD` ← app-db `.password` |
| seed | API | the api's whole non-secret block (`APP_ENV`, `DB_*`, `LOG_FORMAT`, `STORAGE_*`; `CORS_ALLOWED_ORIGINS=https://seed.invalid` — the same config loader validates it, seed-admin serves no HTTP; no `PUBLIC_BASE_URL`, it builds no links) | `DB_PASSWORD`, `AUTH_ENCRYPTION_KEY`, `SEED_ADMIN_PASSWORD` ← seed-admin (used when `--password` is absent) |
| web | UI | `NODE_ENV=production`, `PORT=5000`, `GO_API_URL=http://api.zentax-<cell>.local:3000` (the `api-internal-url` export), `STORAGE_MAX_UPLOAD_BYTES` (optional) | – |

**`CORS_ALLOWED_ORIGINS`.** The api's config loader refuses to boot in
staging/production without an explicit, non-wildcard CORS origin. With a
`publicHostname` in `cdk.json` the default is `https://<publicHostname>`
(`https://eu.staging.zentax.software`); an explicit
`environments.<cell>.corsAllowedOrigins` (comma-separated) wins — use it to add
the Edge stack's `cloudfront-url` next to the custom domain; without a public
hostname the value is the reserved placeholder `https://zentax-<cell>.invalid`.
`PUBLIC_BASE_URL` travels next to it (same `publicHostname` source, see the
table above); the seed task gets neither a real origin nor that variable.
Nothing depends on it in practice: the web tier proxies every browser call to
the api and strips the `Origin` header (ZenTax-UI `server/go-proxy.ts`), so the
api never performs a CORS check for real traffic.

### Rough monthly cost (eu-central-1, on-demand, 730 h, excluding data transfer and free tiers)

This table is the single source of truth for the running cost of the whole
cell; the ops runbook and the UI app's README link here rather than repeating
it. **Owner** says which app's stacks carry the line.

| Line | Owner | staging-eu | production-eu | Assumptions |
|---|---|---|---|---|
| RDS instance | API | ~$27 | ~$107 | t4g.small $0.037/h; t4g.medium $0.073/h × 2 for Multi-AZ |
| RDS storage + backups | API | ~$3 | ~$18 | gp3 $0.13/GB; Multi-AZ doubles storage; 35-day backups a bit beyond the free 100 % |
| NAT gateways | API | ~$38 | ~$76 | $0.052/h each, + $0.052/GB processed (pulls/secrets/logs bypass NAT via endpoints) |
| **Interface endpoints** | API | **~$74** | **~$74** | **4 endpoints (ECR api, ECR dkr, Secrets Manager, Logs) × 2 AZs = 8 ENIs × ~$0.0126/h** + $0.01/GB — the largest fixed line; see note |
| Fargate api | API | ~$21 | ~$42 | 0.5 vCPU + 1 GB ≈ $0.028/h per task |
| Fargate web | UI | ~$10 | ~$21 | 0.25 vCPU + 0.5 GB ≈ $0.014/h per task |
| ALB | UI | ~$23 | ~$25 | $0.027/h + a few LCUs |
| CloudFront | UI | ~$2 | ~$10 | low traffic; first TB and 10 M requests are free-tier |
| **Container Insights** | API (cluster) | **~$10** | **~$15** | billed as custom metrics (~$0.30/metric-month): ~30–50 metrics for a 2-service cluster, more per extra task |
| Logs, PI, flow logs | API (+ web log group: UI) | ~$6 | ~$15 | ingest $0.63/GB; 365-day retention grows over time |
| Alarms + SNS | API 5 / UI 3 (+1 Edge) | ~$1 | ~$1 | 8 alarms in eu-central-1 + 1 in us-east-1 × $0.10; e-mail delivery is free |
| KMS, Secrets Manager, ECR, S3 | API (4 secrets, 2 repos, key) / UI (1 secret, 1 repo) | ~$5 | ~$8 | $1/key, $0.40/secret (5 secrets), ECR $0.10/GB |
| Route 53 + ACM | API (zone, account-level) / UI (certificate) | ~$1 (shared) | — | $0.50/hosted zone + $0.40/M queries; public ACM certificates are free |
| **Total** | | **≈ $220/month** | **≈ $410/month** | production autoscaling to 2× adds up to ~$60 under load |

Note on endpoints: they are the standard "no image pulls or secrets over NAT"
posture and the single largest fixed cost in staging. If staging needs to be
cheaper, the endpoints could be dropped there (traffic then goes through the NAT
at $0.052/GB), which is a small code change in `network-stack.ts`.

## First deployment

### 0. Use an admin identity — never root

The credentials on the dev machine are the account's **root user**. Do not use
them for CDK: root cannot be constrained by IAM policies or permission
boundaries, its actions cannot be attributed to a person, MFA-scoped/short-lived
sessions are unavailable, and CIS / SOC 2 / ISO controls require root usage to be
exceptional and alarmed. Create an IAM Identity Center (or IAM) administrator
with MFA, sign in as that identity, enable a CloudTrail trail, and lock the root
credentials away.

### 1. GitHub federation + the execution policies (admin credentials, before the bootstrap)

```bash
cd infra && export AWS_PROFILE=zentax-admin
npx cdk deploy ZenTax-GithubOidc
# outputs: DeployRoleArn{StagingEu,ProductionEu}{Api,Web} -> the two pipelines' `role-to-assume`
# (each workflow derives the ARN from the account id and the role name zentax-deploy-<cell>-{api,web});
# CfnExecutionPolicyArn + CfnExecutionPolicyEdgeArn -> step 2.
# In BOTH GitHub repositories create the "staging-eu" and "production-eu" environments
# (production-eu with required reviewers; "Deployment branches: main only" on both).
```

This stack uses the CDK's `CliCredentialsStackSynthesizer`: CloudFormation runs
it with the admin's own credentials, not through the bootstrap's execution
role. That is why it can be deployed before the bootstrap exists, why CI (whose
deploy roles have no CloudFormation write permission) can never change it, and
why the execution policies never need — and never get — any permission over
IAM policies, including themselves. Changing the deploy roles or the policies
is always an admin action: edit, `cdk deploy ZenTax-GithubOidc`.

### 1b. The DNS zone (admin credentials; once per account)

```bash
# The two Google values (context.dns.googleSiteVerification, context.dns.googleDkimPublicKey)
# are already in cdk.json; `cdk synth` must print NO dns warning (it warns while either is empty).
npx cdk deploy ZenTax-Dns   # termination protection on; the zone + every record set are RETAINed
# outputs: HostedZoneId  -> zentax-ui/infra/cdk.json, environments.<cell>.hostedZoneId (both cells)
#          NameServers   -> Squarespace: Domains -> zentax.software -> DNS -> use custom nameservers
```

Verify before switching the delegation: `dig @<one of the NameServers> zentax.software MX`,
`… www.zentax.software CNAME`, `… google._domainkey.zentax.software TXT` must
return the values above. Then change the nameservers at Squarespace; the site
and mail keep working because the records are identical. The
`app.zentax.software` / `eu.*.zentax.software` records appear only once the UI
app's Edge stack is deployed with the custom domain on.

### 2. Bootstrap once per account/region with the scoped execution policies

```bash
POLICIES=arn:aws:iam::160117555326:policy/ZenTaxCfnExecutionPolicy,arn:aws:iam::160117555326:policy/ZenTaxCfnExecutionPolicyEdge
# home region (every stack of both apps but Edge)
npx cdk bootstrap aws://160117555326/eu-central-1 --cloudformation-execution-policies "$POLICIES"
# us-east-1 (the UI app's ZenTax-<Title>-Edge: CloudFront alarm, ACM certificate, Route 53 aliases)
npx cdk bootstrap aws://160117555326/us-east-1 --cloudformation-execution-policies "$POLICIES"
```

Bootstrapping creates the `cdk-hnb659fds-*` roles both deploy roles assume, the
assets bucket and the assets ECR repository. Without the flag the execution
role would get `AdministratorAccess` — re-running the command with the flag
replaces the policies on an existing bootstrap, which is also how an account
bootstrapped with only `ZenTaxCfnExecutionPolicy` gets the Edge policy: run it
again with both ARNs, the execution role is updated in place. One bootstrap
serves both apps.

#### Why staging-eu and production-eu share the deploy boundary today

Both cells live in one account and one region, so they share one bootstrap
and therefore **one CloudFormation execution role**. The GitHub deploy roles
are separate — per cell (production-eu's are gated by the GitHub
environment's reviewers) and per repository (the api role cannot push the web
image or roll the web service, and vice versa) — but the moment any of them
runs `cdk deploy`, the template executes with the same execution policies,
which by construction can touch `zentax-*` resources of *either* cell and
*either* half of the cell, can create any `zentax-*` IAM role with any inline
policy, and can change any record in the hosted zone. Name-scoping cannot
express "staging-eu may not touch production-eu" (or "the UI pipeline may not
touch RDS") within one account. The actual boundary is an account: one per
cell (each with its own bootstrap and its own `ZenTax-GithubOidc`), which is
the planned end state — the cdk.json `"// deploy boundary"` comment marks it.
Until then the mitigations are the GitHub environment protection on
production-eu, CloudTrail, the narrow deploy roles, and the fact that each
repository's pipeline only ever names its own stacks.

### 3. Network + Data + Cluster for the cell

```bash
npx cdk deploy --context env=staging-eu ZenTax-StagingEu-Network ZenTax-StagingEu-Data ZenTax-StagingEu-Cluster
```

(`npx cdk deploy --context env=staging-eu --all` also works and deploys in
dependency order, but it would create Api before any image exists — follow the
order below for a first deployment.)

If `alarmEmail` is set, confirm the SNS subscription mail (the UI app's Edge
topic sends its own after the UI deploy).

### 4. Build and push the api + migrate images

Images must exist before the Api stack is created (the service pulls `:latest`
by default; the pipeline pins a SHA with `--context imageTag=<sha>`, see
"Continuous deployment").

```bash
ACCOUNT=160117555326; REGION=eu-central-1; CELL=staging-eu; TAG=$(git rev-parse --short=12 HEAD)
REGISTRY=$ACCOUNT.dkr.ecr.$REGION.amazonaws.com
aws ecr get-login-password --region $REGION | docker login --username AWS --password-stdin $REGISTRY

# from the repository root (Dockerfile and Dockerfile.migrate); cpuArchitecture is X86_64
docker build --platform linux/amd64 -t $REGISTRY/zentax/$CELL/api:$TAG -t $REGISTRY/zentax/$CELL/api:latest .
docker build --platform linux/amd64 -f Dockerfile.migrate -t $REGISTRY/zentax/$CELL/migrate:$TAG -t $REGISTRY/zentax/$CELL/migrate:latest .
docker push --all-tags $REGISTRY/zentax/$CELL/api
docker push --all-tags $REGISTRY/zentax/$CELL/migrate
```

### 5. Run the migrations

The cluster and the `zentax-staging-eu-migrate` task definition exist (step 3);
the migrate image exists (step 4). Using the Network outputs (`PrivateSubnetIds`,
`JobsSecurityGroupId`):

```bash
SUBNETS=$(aws cloudformation describe-stacks --stack-name ZenTax-StagingEu-Network --query "Stacks[0].Outputs[?OutputKey=='PrivateSubnetIds'].OutputValue" --output text)
JOBS_SG=$(aws cloudformation describe-stacks --stack-name ZenTax-StagingEu-Network --query "Stacks[0].Outputs[?OutputKey=='JobsSecurityGroupId'].OutputValue" --output text)
NETCFG="awsvpcConfiguration={subnets=[${SUBNETS//,/,}],securityGroups=[$JOBS_SG],assignPublicIp=DISABLED}"

TASK=$(aws ecs run-task --cluster zentax-staging-eu --launch-type FARGATE \
  --task-definition zentax-staging-eu-migrate --network-configuration "$NETCFG" \
  --query 'tasks[0].taskArn' --output text)
aws ecs wait tasks-stopped --cluster zentax-staging-eu --tasks "$TASK"
aws ecs describe-tasks --cluster zentax-staging-eu --tasks "$TASK" --query 'tasks[0].containers[0].exitCode'   # must be 0
aws logs tail /zentax/staging-eu/migrate --since 10m
```

(The Cluster stack also exports `MigrateTaskDefinitionArn` / `SeedTaskDefinitionArn`
— the exact revision CDK registered — which is what the pipeline runs, never
the family's "latest revision".)

### 6. Api (the service)

```bash
npx cdk deploy --context env=staging-eu ZenTax-StagingEu-Api
# outputs: ApiServiceName, ApiInternalUrl (http://api.zentax-staging-eu.local:3000), ApiTaskDefinitionArn
```

Every export the UI app needs now exists. From here on the **UI pipeline**
(`zentax-ui/.github/workflows/deploy.yml`) creates `ZenTax-StagingEu-Web` +
`ZenTax-StagingEu-Edge` (with the certificate, aliases and redirect once
`hostedZoneId` is set there) and runs the smoke test through CloudFront;
nothing in this repository is involved.

### 7. Seed the first tenant and admin

The password comes from the `zentax/staging-eu/seed-admin` secret (injected as
`SEED_ADMIN_PASSWORD`), so no password is typed, logged or stored in a task
definition. Only the non-secret arguments are passed as a container override:

```bash
aws ecs run-task --cluster zentax-staging-eu --launch-type FARGATE \
  --task-definition zentax-staging-eu-seed --network-configuration "$NETCFG" \
  --overrides '{"containerOverrides":[{"name":"seed","command":["--tenant-slug","acme","--tenant-name","Acme Corp","--email","admin@acme.example","--timezone","Europe/Berlin"]}]}'

# read the initial password once, sign in, change it, then rotate or delete the secret
aws secretsmanager get-secret-value --secret-id zentax/staging-eu/seed-admin --query SecretString --output text
```

(`--password <value>` still works as an explicit override, but it goes through
your shell history and CloudTrail's request parameters — prefer the secret.
The pipeline does the same on `workflow_dispatch` with `seed=true`.)

### 8. (Optional) widen the CORS origin

`CORS_ALLOWED_ORIGINS` already is `https://<publicHostname>`. To also admit the
raw CloudFront origin (the Edge stack's `zentax-staging-eu-cloudfront-url`
export, in us-east-1), set
`environments.staging-eu.corsAllowedOrigins` to both, comma-separated, and
deploy `ZenTax-StagingEu-Api` again — see "Environment contract".

## Continuous deployment (what `deploy.yml` does with `zentax-deploy-<cell>-api`)

1. `plan` resolves the cell — `staging-eu` on a push to `main`, the dispatch input otherwise — and its title with `scripts/env-title.sh` (`staging-eu` → `StagingEu`).
2. `aws-actions/configure-aws-credentials` with `role-to-assume: arn:aws:iam::<account>:role/zentax-deploy-<cell>-api` in the GitHub environment `staging-eu`/`production-eu`.
3. Build + push the api and migrate images tagged with the 12-character commit SHA.
4. `cdk deploy --context env=<cell> --context imageTag=<sha> ZenTax-<Title>-Network ZenTax-<Title>-Data ZenTax-<Title>-Cluster` — the Cluster stack registers the migrate/seed task definitions pointing at the new tag and exports their pinned ARNs.
5. `ecs run-task` the pinned `MigrateTaskDefinitionArn` (after `ecs-pin-taskdef.sh` verified that its roles are `zentax-<cell>-*` and its image is from this account's ECR), wait, assert exit code 0, tail the log.
6. `cdk deploy ... ZenTax-<Title>-Api` — the service rolls to the immutable tag; `describe-task-definition` later shows exactly what runs. (The deploy role can assume the CDK bootstrap roles.)
7. `aws ecs wait services-stable` on the api service.
8. Optionally (dispatch input `seed=true`) the seed task, with the identity values from the environment secrets `SEED_ADMIN_EMAIL`, `SEED_TENANT_SLUG`, `SEED_TENANT_NAME`, `SEED_TIMEZONE` and the password from the seed-admin secret.

The fallback for a manual roll of `:latest` is
`aws ecs update-service --cluster zentax-<cell> --service zentax-<cell>-api --force-new-deployment`;
the circuit breaker rolls back a bad image either way. The web tier is not
part of an api release: `GO_API_URL` is a Cloud Map name, and the web's
security-group rule is on the api *group*, not on tasks.

## Rolling back

- **Automatic:** the service has the ECS deployment circuit breaker with rollback; a deployment whose tasks fail health checks is rolled back to the previous task-definition revision, and the CloudFormation update fails cleanly.
- **Manual (previous image):** in Actions open the last green `deploy.yml` run and choose **Re-run all jobs** (same `main` ref, old commit — the environment's `main`-only branch policy does not allow `workflow_dispatch` on an arbitrary ref), or `git revert` the offending commit and push to `main`; locally, from an admin identity, `cdk deploy --context env=<cell> --context imageTag=<known-good sha> ZenTax-<Title>-Cluster ZenTax-<Title>-Api`; or push/retag the known-good image as `latest` and `aws ecs update-service --cluster zentax-<cell> --service zentax-<cell>-api --force-new-deployment`.
- **Manual (previous task-definition revision):** `aws ecs update-service --cluster zentax-<cell> --service zentax-<cell>-api --task-definition zentax-<cell>-api:<N>`.
- **Database:** migrations are forward-only (ADR-0013); to undo, restore from PITR to a new instance and switch `DB_HOST` — see "Restoring", a runbook item, not a button.
- **Foundations:** a Network/Data/Cluster change that broke something is reverted the same way as any other commit; CloudFormation refuses to remove an export the Web stack still imports, so a foundation rollback can never strand the web tier.
- **DNS:** `ZenTax-Dns` is admin-deployed; a bad record change is a `git revert` + `cdk deploy ZenTax-Dns`. The zone itself is never replaced (RETAIN, and the name servers are pinned at the registrar).

## Restoring the database

RDS keeps `dbBackupDays` of point-in-time recovery (7 staging-eu / 35
production-eu) and the documents bucket keeps non-current object versions for
the same window (ADR-0007 / ADR-0014): one retention window for a full restore.

1. `aws rds restore-db-instance-to-point-in-time --source-db-instance-identifier zentax-<cell> --target-db-instance-identifier zentax-<cell>-restore --restore-time <ISO-8601> --db-subnet-group-name <the Data stack's subnet group> --vpc-security-group-ids <db security group id>` (the `db` group is in the Network stack; `DbEndpoint` and the subnet group are visible in the Data stack's resources) and `aws rds wait db-instance-available`.
2. Verify on the side instance. The master secret still matches (a restore carries the master password); `zentax_app` and its password exist inside the restored data.
3. Cut over by pointing the api at it: the cleanest path is renaming — `modify-db-instance --new-db-instance-identifier` the current instance to `zentax-<cell>-old` and the restore to `zentax-<cell>`, so `DB_HOST` (the instance endpoint address, derived from the identifier) is unchanged for the next `ZenTax-<Title>-Api` deploy. Delete `zentax-<cell>-old` when done (it is billed hourly).
4. Documents: `aws s3api list-object-versions` on the affected prefix and copy the wanted version back over the current key (versioning never deletes the older version during the window).

## Destroying staging-eu

Order matters across the two apps: the UI stacks import this app's exports, so
they must go first.

```bash
# 1. in zentax-ui/infra:  npx cdk destroy --context env=staging-eu ZenTax-StagingEu-Edge ZenTax-StagingEu-Web
# 2. here:
npx cdk destroy --context env=staging-eu ZenTax-StagingEu-Api ZenTax-StagingEu-Cluster ZenTax-StagingEu-Data ZenTax-StagingEu-Network
```

Staging resources carry `DESTROY` removal policies (the documents bucket
auto-empties, ECR repos empty on delete, RDS has no deletion protection, is
deleted without a final snapshot and its automated backups are deleted).
Secrets enter the 30-day recovery window;
`aws secretsmanager delete-secret --force-delete-without-recovery` if the same
names must be recreated sooner. The `RDSOSMetrics` log group goes with the
owning cell's Data stack (enhanced monitoring recreates it, without a
retention, on the next write). `ZenTax-Dns` and `ZenTax-GithubOidc` are not
part of a cell and stay.

## Decommissioning production-eu (snapshot and delete — never a flag flip)

Production is `RETAIN` everywhere that holds data, the RDS instance carries
`DeletionPolicy: Snapshot` and deletion protection. There is deliberately no
setting that makes `cdk destroy` erase production; the procedure is manual and
leaves a restorable copy at every step:

1. **Snapshot first, independently of CloudFormation:**
   `aws rds create-db-snapshot --db-instance-identifier zentax-production-eu --db-snapshot-identifier zentax-production-eu-final-$(date +%Y%m%d)`
   and `aws rds wait db-snapshot-available ...`. Export the documents bucket if
   the data must outlive the account (`aws s3 sync s3://zentax-production-eu-documents-... <archive>`).
2. **Lift the API-level guards only now:** set `deletionProtection: false` for
   production-eu in `cdk.json` (here for RDS, and in the UI app for the ALB) and
   deploy `ZenTax-ProductionEu-Data` (and the UI app's `ZenTax-ProductionEu-Web`).
   The removal policies are unchanged by this: RDS is still `Snapshot`,
   everything else `Retain`.
3. Destroy the UI stacks first (`ZenTax-ProductionEu-Edge`, `ZenTax-ProductionEu-Web`,
   from `zentax-ui/infra`), then here
   `cdk destroy --context env=production-eu ZenTax-ProductionEu-Api ZenTax-ProductionEu-Cluster ZenTax-ProductionEu-Data ZenTax-ProductionEu-Network`.
   CloudFormation takes a **second final snapshot** of RDS and deletes the
   instance (automated backups are kept for their retention); the buckets, ECR
   repositories, secrets, KMS key and log groups are **left in place**.
4. Delete the retained resources by hand, in this order, once the snapshots are
   verified: empty + delete the buckets (documents here, ALB logs in the UI
   app), delete the three ECR repositories, `delete-secret` (30-day recovery
   window unless forced), delete the log groups, and finally
   `aws kms schedule-key-deletion --pending-window-in-days 30` on the data key
   — after the last object encrypted with it is gone, never before.
5. Delete the final snapshots when the retention obligation (ADR-0014) has expired.

## Notes and caveats

- **Performance Insights** is enabled on `db.t4g.small`; PostgreSQL supports it on
  every instance class (the small-instance exclusion applies to MySQL/MariaDB).
- The CloudFront origin-facing prefix list ID is region-specific and resolved at
  deploy time by a small custom resource in the Network stack
  (`Custom::CloudFrontOriginFacingPrefixList`, logging to
  `/zentax/<cell>/cdk/cloudfront-prefix-list-lookup`); set
  `environments.<cell>.cloudFrontPrefixListId` to skip it. It stays here, with
  the ALB security group it feeds, even though the ALB itself is the UI app's.
- The RDS connection/memory alarm thresholds come from a table of instance-class
  RAM in `lib/alarms.ts`; a class missing from it fails synth on purpose.
- `cdk.context.json` is intentionally absent: AZs are configured and the hosted
  zone is created (never `fromLookup`), so synth performs no lookups. If one
  ever appears, something started needing AWS at synth time.
- The built-in CloudFormation validator prints `W3010` ("avoid hard-coding
  availability zones") for the VPC subnets during synth/tests. It is informational
  and the direct consequence of the no-lookup choice above. Likewise the two
  `ZenTax-Dns` warnings while the Google DNS values in `cdk.json` are empty.
- `lib/config.ts` refuses the UI app's context keys (`webDesiredCount`,
  `originVerifyVersion`, `domainName`, `certificateArn`, `hostedZoneId`,
  `hostedZoneName`, `entryHostnames`, `maxUploadBytes`) and the retired
  `githubRepositories`, so a setting that would silently do nothing here cannot
  linger in `cdk.json`. It also refuses a cell key that is not
  `<tier>-<regionLabel>` (a leftover `staging` fails synth) and a
  `publicHostname` that is not a lowercase DNS name.
