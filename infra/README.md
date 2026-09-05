# ZenTax API infrastructure (AWS CDK v2, TypeScript)

Infrastructure-as-code for the **API half** of the ZenTax SaaS *cell* (ADR-0005,
ADR-0024): the shared foundations every environment is built on, the Go api
service, and the account-level GitHub Actions federation (ADR-0016). Everything
here is in `infra/` and has its own `package.json`; the Go module is untouched.

The web half — the web service, the ALB, CloudFront and the CloudFront alarm —
is the CDK app of the UI repository (`github.com/mohdhallal/zentax-ui`,
`infra/`). The two apps never reference each other's constructs: the UI app
reads this app's stacks **only through the CloudFormation export contract**
below (`lib/exports.ts`, identical in both repositories).

```
infra/
  bin/zentax.ts            CDK app entry: --context env=staging|production [--context imageTag=<sha>]
  cdk.json                 per-environment settings (context.environments.*) + feature flags; "// ..." keys are comments
  lib/config.ts            typed loader/validator for that context (refuses the UI app's keys)
  lib/exports.ts           THE EXPORT CONTRACT — same file as zentax-ui/infra/lib/exports.ts, byte for byte
  lib/network-stack.ts     ZenTax-<Env>-Network  VPC, endpoints, ALL security groups (incl. alb + web), flow-log group
  lib/data-stack.ts        ZenTax-<Env>-Data     KMS, RDS Postgres 16, secrets, documents bucket, api + migrate ECR, log groups, alarm topic + RDS alarms
  lib/cluster-stack.ts     ZenTax-<Env>-Cluster  ECS cluster + Cloud Map namespace, migrate + seed task definitions
  lib/api-stack.ts         ZenTax-<Env>-Api      Fargate api service (Cloud Map "api"), its roles + grants, running-task alarm
  lib/alarms.ts            alarm topic / wiring helpers, RDS max_connections table
  lib/ecs-roles.ts         task/execution role naming shared by Cluster and Api
  lib/github-oidc-stack.ts ZenTax-GithubOidc     GitHub OIDC provider, api + web deploy roles per environment, ZenTaxCfnExecutionPolicy
  test/                    jest + aws-cdk-lib/assertions (both environments, the contract, the split rules)
```

```bash
cd infra
npm ci
npm test                                   # 108 tests over staging + production + OIDC + the export contract
npx cdk synth --context env=staging        # no AWS calls: account/region/AZs are explicit
npx cdk synth --context env=production
npx cdk synth --context env=staging --context imageTag=$(git rev-parse --short=12 HEAD)   # what the pipeline does
npx cdk ls                                 # omit env to list every stack
```

## Which repository owns what

| | API repository (this app) | UI repository (`zentax-ui/infra`) |
|---|---|---|
| Stacks | `ZenTax-GithubOidc` (account-level); per environment `ZenTax-<Env>-Network`, `-Data`, `-Cluster`, `-Api` | per environment `ZenTax-<Env>-Web` (eu-central-1), `-Edge` (us-east-1) |
| Images | `zentax/<env>/api`, `zentax/<env>/migrate` (ECR in the Data stack) | `zentax/<env>/web` (ECR in the Web stack) |
| Services / tasks | api service; migrate + seed one-off tasks | web service |
| Front door | — | ALB (+ access-log bucket), origin-verify secret, CloudFront, optional custom domain + Route 53 records |
| Security groups | **all** of them (alb, web, api, jobs, db) — see "Stack layout and why" | imports `alb-sg-id` / `web-sg-id` |
| Alarms | RDS (Data), api running tasks (Api); the home-region topic | web running tasks, ALB 5xx / unhealthy hosts (Web, on the imported topic); CloudFront 5xx + the us-east-1 topic (Edge) |
| Deploy role | `zentax-deploy-<env>-api` (trusts `repo:mohdhallal/zentax-api:environment:<env>`) | `zentax-deploy-<env>-web` (trusts `repo:mohdhallal/zentax-ui:environment:<env>`) — created **here**, in `ZenTax-GithubOidc` |
| Pipeline | `.github/workflows/deploy.yml`: images → Network+Data+Cluster → migrate → Api → services-stable (→ seed) | images → Web+Edge → services-stable → smoke through CloudFront |

Deploy order on a fresh cell: this app first (it creates every export the Web
stack imports), then the UI pipeline. Afterwards the two pipelines are
independent: an api release never touches the web tier (the web finds the api
by its Cloud Map name), a web release never touches the api.

## The export contract (`lib/exports.ts`)

Every value the UI app needs is a CloudFormation export named
`zentax-<env>-<key>` (account-level OIDC exports aside). The UI app consumes
them with `cdk.Fn.importValue(exportName(env, key))` and the `from*Attributes`
/ `from*Id` / `fromTopicArn` factories — no context lookups, no
`Fn::GetStackOutput` into these stacks. CloudFormation refuses to delete or
change an export another stack imports, which is the safety net: a rename is
"add the new name, move the importers, remove the old name", never an in-place
edit. Change the file in both repositories in the same change (a test here
compares it with `../zentax-ui/infra/lib/exports.ts` when that checkout is
present).

| Stack (emitter) | Key | Value |
|---|---|---|
| `ZenTax-<Env>-Network` (API) | `vpc-id` | VPC ID |
| | `vpc-cidr` | VPC CIDR block |
| | `availability-zones` | comma-separated list, e.g. `eu-central-1a,eu-central-1b` |
| | `public-subnet-ids` | comma-separated subnet IDs, in availability-zone order (the ALB) |
| | `private-subnet-ids` | comma-separated subnet IDs, in availability-zone order (services, `ecs run-task`) |
| | `alb-sg-id` | security group of the public ALB (CloudFront prefix list :80 only) |
| | `web-sg-id` | security group of the web tasks (from alb :5000) |
| | `api-sg-id` | security group of the api tasks (from web :3000) |
| | `jobs-sg-id` | security group of the migrate/seed tasks (no inbound) |
| `ZenTax-<Env>-Data` (API) | `alarm-topic-arn` | the environment's SNS alarm topic (home region) |
| | `documents-bucket` | documents bucket name |
| | `ecr-api` | repository URI `<account>.dkr.ecr.<region>.amazonaws.com/zentax/<env>/api` |
| | `ecr-migrate` | repository URI of `zentax/<env>/migrate` |
| `ZenTax-<Env>-Cluster` (API) | `cluster-name` | ECS cluster name `zentax-<env>` |
| | `cluster-arn` | ECS cluster ARN |
| | `namespace-name` | Cloud Map private DNS namespace, `zentax-<env>.local` |
| | `namespace-id` | Cloud Map namespace ID |
| | `namespace-arn` | Cloud Map namespace ARN |
| | `migrate-task-family` | `zentax-<env>-migrate` |
| | `migrate-task-arn` | pinned task-definition revision ARN (never the family) |
| | `seed-task-family` | `zentax-<env>-seed` |
| | `seed-task-arn` | pinned task-definition revision ARN |
| | `seed-admin-secret-arn` | `zentax/<env>/seed-admin` (the first admin's initial password) |
| `ZenTax-<Env>-Api` (API) | `api-service-name` | `zentax-<env>-api` |
| | `api-internal-url` | `http://api.zentax-<env>.local:3000` — what the web tier proxies to (`GO_API_URL`) |
| | `api-task-arn` | pinned task-definition revision ARN |
| `ZenTax-<Env>-Web` (UI) | `web-service-name` | `zentax-<env>-web` |
| | `cloudfront-url` | `https://<distribution>.cloudfront.net` |
| | `cloudfront-id` | distribution ID |
| | `alb-dns` | ALB DNS name |
| | `ecr-web` | repository URI of `zentax/<env>/web` |
| | `web-task-arn` | pinned task-definition revision ARN |
| `ZenTax-GithubOidc` (API, account-level) | `zentax-cfn-execution-policy-arn` | `ZenTaxCfnExecutionPolicy` — pass to `cdk bootstrap --cloudformation-execution-policies` |
| | `zentax-deploy-role-<env>-api` | role ARN for the API repository's GitHub environment `<env>` |
| | `zentax-deploy-role-<env>-web` | role ARN for the UI repository's GitHub environment `<env>` |

The pipelines read the same values through the CloudFormation **output keys**
(`scripts/stack-output.sh`, by stack name): `ClusterName`, `ApiServiceName`,
`MigrateTaskDefinitionArn`, `SeedTaskDefinitionArn`, `SeedAdminSecretArn`,
`PrivateSubnetIds`, `JobsSecurityGroupId`, `ApiRepositoryUri`,
`MigrateRepositoryUri`.

## Stack layout and why

Four stacks per environment here, `Network -> Data -> Cluster -> Api`, then the
UI app's `Web -> Edge` on top:

- **Network** owns every security group and the rules between them — including
  the ALB's and the web tasks' groups, although those resources are created by
  the other app. RDS (Data) must admit the api tasks (Api) while being created
  first, and the web group must admit the ALB's group, which belongs to a stack
  of another app: the groups cannot live next to their consumers without a
  dependency cycle, and a group defined in one app cannot reference a group
  defined in the other at synth time. The Web stack imports `alb-sg-id` and
  `web-sg-id` and attaches them.
- **Data** is everything stateful or expensive to recreate, plus the
  environment's alarm topic and the RDS alarms (they live next to the
  instance). The web tier's state — its ECR repository, its log group and the
  origin-verify secret CloudFront and the ALB share — lives with the ALB, in
  the Web stack.
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

Cross-stack references *inside* this app use CloudFormation's weak
`Fn::GetStackOutput` (the default `@aws-cdk/core:defaultCrossStackReferences:
"weak"` for new CDK apps), so a producer stack can change a value without the
consumer blocking the update. References *between* the apps are the exports
above — strong by nature, which is what protects the contract.

A second account per environment is a context change: set
`environments.<env>.account` (and `region`) in both apps, bootstrap that
account, and deploy that account's own `ZenTax-GithubOidc`.

## What gets created

Per environment (`eu-central-1`, account `160117555326` by default). The
**Owner** column says which app creates the line; the UI app's lines are listed
for completeness so this table stays the one picture of the cell.

| Component | Owner | Staging | Production |
|---|---|---|---|
| VPC | API | `10.20.0.0/16`, 2 AZs, 2 public /24 + 2 private /22, **1 NAT** | `10.30.0.0/16`, same layout, **2 NATs** |
| VPC endpoints | API | S3 gateway; interface: ECR api, ECR dkr, Secrets Manager, CloudWatch Logs | same |
| Security groups | API | `alb` (CloudFront origin-facing prefix list :80 only), `web` (from alb :5000), `api` (from web :3000), `jobs` (no inbound), `db` (from api + jobs :5432) | same |
| RDS PostgreSQL 16 | API | `db.t4g.small`, single-AZ, 20 GB gp3 (autoscale to 100), 7-day PITR, **no** deletion protection, `DESTROY` on stack delete, automated backups deleted with it | `db.t4g.medium`, **Multi-AZ**, 50 GB gp3 (to 200), **35-day PITR**, deletion protection, **`SNAPSHOT`** on delete (never a plain delete), automated backups kept |
| RDS extras (both) | API | CMK-encrypted storage, parameter group `rds.force_ssl=1`, slow-statement + connection logging with `log_parameter_max_length(_on_error)=0` (bind values never reach the log export), Performance Insights (7 days, CMK), enhanced monitoring 60 s (`RDSOSMetrics` log group pre-created with the retention by the owning environment — see cdk.json `ownsRdsOsMetricsLogGroup`), `postgresql` log export to `/aws/rds/instance/zentax-<env>/postgresql`, RDS CA `rds-ca-rsa2048-g1` | |
| KMS | API | `alias/zentax/<env>/data`, rotation on | same, `RETAIN` |
| Secrets Manager | API | `zentax/<env>/db-master` (generated by RDS), `zentax/<env>/app-db` `{username: zentax_app, password}`, `zentax/<env>/auth-encryption-key` (64 chars, no punctuation), `zentax/<env>/seed-admin` (32 chars, the first admin's initial password) | same, `RETAIN` |
| Secrets Manager | UI | `zentax/<env>/origin-verify` (the CloudFront → ALB shared header value) | same |
| S3 | API | `zentax-<env>-documents-<acct>-<region>`: SSE-KMS (CMK, bucket key), versioned, block-all-public, TLS-only policy, non-current versions expire after **`dbBackupDays` (7 d)** — one retention window with the database (ADR-0007/0014), incomplete multipart aborted 7 d | same, non-current versions **35 d**, `RETAIN` |
| S3 | UI | `zentax-<env>-alb-logs-…` (ALB access logs, expire with log retention) | same |
| ECR | API | `zentax/<env>/api`, `zentax/<env>/migrate`: scan on push, keep last 30 tagged, untagged expire 7 d | same, `RETAIN` |
| ECR | UI | `zentax/<env>/web`, same rules | same |
| CloudWatch Logs | API | `/zentax/<env>/{api,migrate,seed,vpc-flow-logs,cdk/cloudfront-prefix-list-lookup}`, `/aws/rds/instance/zentax-<env>/postgresql`, `RDSOSMetrics` (owner only) — every group explicit, **30 days** | **365 days** |
| CloudWatch Logs | UI | `/zentax/<env>/web` | |
| ECS (Cluster stack) | API | cluster `zentax-<env>`, Container Insights, Cloud Map namespace `zentax-<env>.local` | same |
| api service | API | 0.5 vCPU / 1 GB, **1 task**, autoscale 1–2 on CPU 60 %, circuit breaker + rollback, Cloud Map `api.zentax-<env>.local` | **2 tasks**, autoscale 2–4 |
| web service | UI | 0.25 vCPU / 0.5 GB, **1 task**, autoscale 1–2 | **2 tasks**, autoscale 2–4 |
| One-off jobs (Cluster stack) | API | `zentax-<env>-migrate` (migrate image) and `zentax-<env>-seed` (api image, entrypoint `seed-admin`, `SEED_ADMIN_PASSWORD` from the seed-admin secret) task definitions, 0.25 vCPU / 0.5 GB; families and pinned-revision ARNs exported | same |
| ALB + CloudFront | UI | internet-facing HTTP:80 ALB (default 403, one rule on `X-Origin-Verify-v<n>`), CloudFront with the default certificate (see the UI app's README) | same |
| IAM | API | task/execution roles `zentax-<env>-{api,migrate,seed}-{task,exec}`; api task role: `s3:GetObject/PutObject/DeleteObject/AbortMultipartUpload` on the documents bucket's objects + `s3:ListBucket` on the bucket (no `*Version` action: versioning is the undo log) + encrypt/decrypt on the CMK, nothing else | same |
| IAM | UI | `zentax-<env>-web-{task,exec}` | same |
| VPC flow logs | API | REJECT traffic to `/zentax/<env>/vpc-flow-logs` | same |
| Alarms (SNS `zentax-<env>-alarms`, optional e-mail via context `alarmEmail`) | API | RDS: CPU > 80 % (5 min), free storage < 10 % of the allocated GB, connections > 80 % of the class's default `max_connections` (`LEAST(RAM/9531392, 5000)`: 225 for t4g.small), freeable memory < 10 % of the class RAM; ECS: api `RunningTaskCount` < desired for 5 min (Container Insights); all notify on ALARM and OK | same (450 connections for t4g.medium) |
| Alarms | UI | web `RunningTaskCount`, ALB `HTTPCode_ELB_5XX_Count` > 10 / 5 min, `UnHealthyHostCount` > 0 (on the imported topic); Edge (us-east-1): CloudFront `5xxErrorRate` > 5 % + its own topic | same |

Account-level (`ZenTax-GithubOidc`, region-agnostic, deployed with the admin's
own credentials — see "First deployment"): the
`token.actions.githubusercontent.com` OIDC provider, **four** deploy roles and
the managed policy `ZenTaxCfnExecutionPolicy`.

| Role | Trusts | May |
|---|---|---|
| `zentax-deploy-<env>-api` | `repo:mohdhallal/zentax-api:environment:<env>` | ECR login + push to `zentax/<env>/{api,migrate}`; `ecs:RunTask` on the `zentax-<env>-migrate` / `-seed` task definitions in cluster `zentax-<env>`; describe/stop tasks; register/describe task definitions (the pin script); update/describe the **api** service; `iam:PassRole` on `zentax-<env>-{api,migrate,seed}-*` to ECS only; read the api/migrate/seed logs |
| `zentax-deploy-<env>-web` | `repo:mohdhallal/zentax-ui:environment:<env>` | ECR login + push to `zentax/<env>/web`; update/describe the **web** service; `iam:PassRole` on `zentax-<env>-web-*` to ECS only; read the web logs |
| both | | `sts:AssumeRole` on the four `cdk-hnb659fds-*` bootstrap roles (the CDK deploy path), the bootstrap version parameter, and read-only `cloudformation:DescribeStacks`/`ListStacks` (on `*`: the CLI and the workflow scripts call them without a stack name) |

`Resource: "*"` appears only for actions that have no resource-level scope. The
repositories are constants in `github-oidc-stack.ts` (`DEPLOY_REPOSITORIES`),
not context: a role is one repository × one environment × one half of the cell.

`ZenTaxCfnExecutionPolicy` is what CloudFormation itself may do while deploying
**any** ZenTax stack — this app's and the UI app's, since both run through the
same bootstrap; it replaces the bootstrap's default `AdministratorAccess`.
EC2 is limited to the network plane (VPC/subnets/routing/gateways/security
groups/endpoints/flow logs — no instances or volumes); S3, ECR, Secrets
Manager, Logs, SNS, CloudWatch, Lambda, SSM, ELB and RDS are scoped to the
ZenTax names (`zentax-*`, `zentax/*`, `/zentax/*`, plus `ZenTax-*` for
CloudFormation-generated names); IAM role management and `PassRole` only for
role names `zentax-*`, `ZenTax-*` and `cdk-*`, and no managed-policy, user,
group or OIDC-provider action at all (so the policy can never widen itself).
ECS, Service Discovery, Application Auto Scaling, CloudFront and KMS are
service-wide because their physical names are generated. It fits IAM's
6144-character cap (a test checks). A missing permission shows up as an
`AccessDenied` in the stack events: extend the statement in
`github-oidc-stack.ts` and redeploy `ZenTax-GithubOidc` — the ARN stays.

### Environment contract (what the containers receive)

| Container | Owner | Environment | Secrets (`ValueFrom`, never a value in the template) |
|---|---|---|---|
| api | API | `APP_ENV`, `DB_HOST`, `DB_PORT`, `DB_NAME=zentax`, `DB_USER=zentax_app`, `DB_SSLMODE=require`, `CORS_ALLOWED_ORIGINS` (context `corsAllowedOrigins`, see below), `STORAGE_DRIVER=s3`, `STORAGE_S3_BUCKET`, `STORAGE_S3_REGION`, `LOG_FORMAT=json` | `DB_PASSWORD` ← app-db `.password`, `AUTH_ENCRYPTION_KEY` ← auth-encryption-key |
| migrate | API | `PGHOST`, `PGPORT`, `PGDATABASE=zentax`, `PGSSLMODE=require`, `APP_DB_USER=zentax_app` | `PGUSER`/`PGPASSWORD` ← db-master, `APP_DB_PASSWORD` ← app-db `.password` |
| seed | API | the api's whole non-secret block (`APP_ENV`, `DB_*`, `LOG_FORMAT`, `STORAGE_*`; `CORS_ALLOWED_ORIGINS=https://seed.invalid` — the same config loader validates it, seed-admin serves no HTTP) | `DB_PASSWORD`, `AUTH_ENCRYPTION_KEY`, `SEED_ADMIN_PASSWORD` ← seed-admin (used when `--password` is absent) |
| web | UI | `NODE_ENV=production`, `PORT=5000`, `GO_API_URL=http://api.zentax-<env>.local:3000` (the `api-internal-url` export), `STORAGE_MAX_UPLOAD_BYTES` (optional) | – |

**`CORS_ALLOWED_ORIGINS` after the split.** The api's config loader refuses to
boot in staging/production without an explicit, non-wildcard CORS origin. That
origin — the CloudFront domain or the custom domain — is created by the UI
app, after this app, so it is *configured* here (`environments.<env>.corsAllowedOrigins`,
comma-separated) rather than referenced. Until it is set the value is the
reserved placeholder `https://zentax-<env>.invalid`. Nothing depends on it in
practice: the web tier proxies every browser call to the api and strips the
`Origin` header (ZenTax-UI `server/go-proxy.ts`), so the api never performs a
CORS check for real traffic. Once the UI pipeline has run, set the key to the
`cloudfront-url` export (or the custom domain) and deploy `ZenTax-<Env>-Api`.

### Rough monthly cost (eu-central-1, on-demand, 730 h, excluding data transfer and free tiers)

This table is the single source of truth for the running cost of the whole
cell; the ops runbook and the UI app's README link here rather than repeating
it. **Owner** says which app's stacks carry the line.

| Line | Owner | Staging | Production | Assumptions |
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

### 1. GitHub federation + the execution policy (admin credentials, before the bootstrap)

```bash
cd infra && export AWS_PROFILE=zentax-admin
npx cdk deploy ZenTax-GithubOidc
# outputs: DeployRoleArn{Staging,Production}{Api,Web} -> the two pipelines' `role-to-assume`
# (each workflow derives the ARN from the account id and the role name);
# CfnExecutionPolicyArn -> next step.
# In BOTH GitHub repositories create the "staging" and "production" environments
# (production with required reviewers; "Deployment branches: main only" on both).
```

This stack uses the CDK's `CliCredentialsStackSynthesizer`: CloudFormation runs
it with the admin's own credentials, not through the bootstrap's execution
role. That is why it can be deployed before the bootstrap exists, why CI (whose
deploy roles have no CloudFormation write permission) can never change it, and
why `ZenTaxCfnExecutionPolicy` never needs — and never gets — any permission
over IAM policies, including itself. Changing the deploy roles or the policy is
always an admin action: edit, `cdk deploy ZenTax-GithubOidc`.

### 2. Bootstrap once per account/region with the scoped execution policy

```bash
# home region (every stack of both apps but Edge)
npx cdk bootstrap aws://160117555326/eu-central-1 \
  --cloudformation-execution-policies arn:aws:iam::160117555326:policy/ZenTaxCfnExecutionPolicy
# us-east-1 (the UI app's ZenTax-<Env>-Edge: the CloudFront alarm)
npx cdk bootstrap aws://160117555326/us-east-1 \
  --cloudformation-execution-policies arn:aws:iam::160117555326:policy/ZenTaxCfnExecutionPolicy
```

Bootstrapping creates the `cdk-hnb659fds-*` roles both deploy roles assume, the
assets bucket and the assets ECR repository. Without the flag the execution
role would get `AdministratorAccess` — re-running the command with the flag
replaces the policy on an existing bootstrap. One bootstrap serves both apps.

#### Why staging and production share the deploy boundary today

Both environments live in one account and one region, so they share one
bootstrap and therefore **one CloudFormation execution role**. The GitHub
deploy roles are separate — per environment (production's are gated by the
GitHub environment's reviewers) and per repository (the api role cannot push
the web image or roll the web service, and vice versa) — but the moment any of
them runs `cdk deploy`, the template executes with the same
`ZenTaxCfnExecutionPolicy`, which by construction can touch `zentax-*`
resources of *either* environment and *either* half of the cell, and can
create any `zentax-*` IAM role with any inline policy. Name-scoping cannot
express "staging may not touch production" (or "the UI pipeline may not touch
RDS") within one account. The actual boundary is an account: one per
environment (each with its own bootstrap and its own `ZenTax-GithubOidc`),
which is the planned end state — the cdk.json `"// deploy boundary"` comment
marks it. Until then the mitigations are the GitHub environment protection on
production, CloudTrail, the narrow deploy roles, and the fact that each
repository's pipeline only ever names its own stacks.

### 3. Network + Data + Cluster for the environment

```bash
npx cdk deploy --context env=staging ZenTax-Staging-Network ZenTax-Staging-Data ZenTax-Staging-Cluster
```

(`npx cdk deploy --context env=staging --all` also works and deploys in
dependency order, but it would create Api before any image exists — follow the
order below for a first deployment.)

If `alarmEmail` is set, confirm the SNS subscription mail (the UI app's Edge
topic sends its own after the UI deploy).

### 4. Build and push the api + migrate images

Images must exist before the Api stack is created (the service pulls `:latest`
by default; the pipeline pins a SHA with `--context imageTag=<sha>`, see
"Continuous deployment").

```bash
ACCOUNT=160117555326; REGION=eu-central-1; TAG=$(git rev-parse --short=12 HEAD)
REGISTRY=$ACCOUNT.dkr.ecr.$REGION.amazonaws.com
aws ecr get-login-password --region $REGION | docker login --username AWS --password-stdin $REGISTRY

# from the repository root (Dockerfile and Dockerfile.migrate); cpuArchitecture is X86_64
docker build --platform linux/amd64 -t $REGISTRY/zentax/staging/api:$TAG -t $REGISTRY/zentax/staging/api:latest .
docker build --platform linux/amd64 -f Dockerfile.migrate -t $REGISTRY/zentax/staging/migrate:$TAG -t $REGISTRY/zentax/staging/migrate:latest .
docker push --all-tags $REGISTRY/zentax/staging/api
docker push --all-tags $REGISTRY/zentax/staging/migrate
```

### 5. Run the migrations

The cluster and the `zentax-staging-migrate` task definition exist (step 3);
the migrate image exists (step 4). Using the Network outputs (`PrivateSubnetIds`,
`JobsSecurityGroupId`):

```bash
SUBNETS=$(aws cloudformation describe-stacks --stack-name ZenTax-Staging-Network --query "Stacks[0].Outputs[?OutputKey=='PrivateSubnetIds'].OutputValue" --output text)
JOBS_SG=$(aws cloudformation describe-stacks --stack-name ZenTax-Staging-Network --query "Stacks[0].Outputs[?OutputKey=='JobsSecurityGroupId'].OutputValue" --output text)
NETCFG="awsvpcConfiguration={subnets=[${SUBNETS//,/,}],securityGroups=[$JOBS_SG],assignPublicIp=DISABLED}"

TASK=$(aws ecs run-task --cluster zentax-staging --launch-type FARGATE \
  --task-definition zentax-staging-migrate --network-configuration "$NETCFG" \
  --query 'tasks[0].taskArn' --output text)
aws ecs wait tasks-stopped --cluster zentax-staging --tasks "$TASK"
aws ecs describe-tasks --cluster zentax-staging --tasks "$TASK" --query 'tasks[0].containers[0].exitCode'   # must be 0
aws logs tail /zentax/staging/migrate --since 10m
```

(The Cluster stack also exports `MigrateTaskDefinitionArn` / `SeedTaskDefinitionArn`
— the exact revision CDK registered — which is what the pipeline runs, never
the family's "latest revision".)

### 6. Api (the service)

```bash
npx cdk deploy --context env=staging ZenTax-Staging-Api
# outputs: ApiServiceName, ApiInternalUrl (http://api.zentax-staging.local:3000), ApiTaskDefinitionArn
```

Every export the UI app needs now exists. From here on the **UI pipeline**
(`zentax-ui/.github/workflows/deploy.yml`) creates `ZenTax-Staging-Web` +
`ZenTax-Staging-Edge` and runs the smoke test through CloudFront; nothing in
this repository is involved.

### 7. Seed the first tenant and admin

The password comes from the `zentax/staging/seed-admin` secret (injected as
`SEED_ADMIN_PASSWORD`), so no password is typed, logged or stored in a task
definition. Only the non-secret arguments are passed as a container override:

```bash
aws ecs run-task --cluster zentax-staging --launch-type FARGATE \
  --task-definition zentax-staging-seed --network-configuration "$NETCFG" \
  --overrides '{"containerOverrides":[{"name":"seed","command":["--tenant-slug","acme","--tenant-name","Acme Corp","--email","admin@acme.example","--timezone","Europe/Berlin"]}]}'

# read the initial password once, sign in, change it, then rotate or delete the secret
aws secretsmanager get-secret-value --secret-id zentax/staging/seed-admin --query SecretString --output text
```

(`--password <value>` still works as an explicit override, but it goes through
your shell history and CloudTrail's request parameters — prefer the secret.
The pipeline does the same on `workflow_dispatch` with `seed=true`.)

### 8. Set the CORS origin

Once the UI pipeline has published `zentax-staging-cloudfront-url`, put it in
`cdk.json` (`environments.staging.corsAllowedOrigins`) and deploy
`ZenTax-Staging-Api` again — see "Environment contract".

## Continuous deployment (what `deploy.yml` does with `zentax-deploy-<env>-api`)

1. `aws-actions/configure-aws-credentials` with `role-to-assume: arn:aws:iam::<account>:role/zentax-deploy-<env>-api` in the GitHub environment `staging`/`production`.
2. Build + push the api and migrate images tagged with the 12-character commit SHA.
3. `cdk deploy --context env=<env> --context imageTag=<sha> ZenTax-<Env>-Network ZenTax-<Env>-Data ZenTax-<Env>-Cluster` — the Cluster stack registers the migrate/seed task definitions pointing at the new tag and exports their pinned ARNs.
4. `ecs run-task` the pinned `MigrateTaskDefinitionArn` (after `ecs-pin-taskdef.sh` verified that its roles are `zentax-<env>-*` and its image is from this account's ECR), wait, assert exit code 0, tail the log.
5. `cdk deploy ... ZenTax-<Env>-Api` — the service rolls to the immutable tag; `describe-task-definition` later shows exactly what runs. (The deploy role can assume the CDK bootstrap roles.)
6. `aws ecs wait services-stable` on the api service.
7. Optionally (dispatch input `seed=true`) the seed task, with the identity values from the environment secrets `SEED_ADMIN_EMAIL`, `SEED_TENANT_SLUG`, `SEED_TENANT_NAME`, `SEED_TIMEZONE` and the password from the seed-admin secret.

The fallback for a manual roll of `:latest` is
`aws ecs update-service --cluster zentax-<env> --service zentax-<env>-api --force-new-deployment`;
the circuit breaker rolls back a bad image either way. The web tier is not
part of an api release: `GO_API_URL` is a Cloud Map name, and the web's
security-group rule is on the api *group*, not on tasks.

## Rolling back

- **Automatic:** the service has the ECS deployment circuit breaker with rollback; a deployment whose tasks fail health checks is rolled back to the previous task-definition revision, and the CloudFormation update fails cleanly.
- **Manual (previous image):** in Actions open the last green `deploy.yml` run and choose **Re-run all jobs** (same `main` ref, old commit — the environment's `main`-only branch policy does not allow `workflow_dispatch` on an arbitrary ref), or `git revert` the offending commit and push to `main`; locally, from an admin identity, `cdk deploy --context env=<env> --context imageTag=<known-good sha> ZenTax-<Env>-Cluster ZenTax-<Env>-Api`; or push/retag the known-good image as `latest` and `aws ecs update-service --cluster zentax-<env> --service zentax-<env>-api --force-new-deployment`.
- **Manual (previous task-definition revision):** `aws ecs update-service --cluster zentax-<env> --service zentax-<env>-api --task-definition zentax-<env>-api:<N>`.
- **Database:** migrations are forward-only (ADR-0013); to undo, restore from PITR to a new instance and switch `DB_HOST` — see "Restoring", a runbook item, not a button.
- **Foundations:** a Network/Data/Cluster change that broke something is reverted the same way as any other commit; CloudFormation refuses to remove an export the Web stack still imports, so a foundation rollback can never strand the web tier.

## Restoring the database

RDS keeps `dbBackupDays` of point-in-time recovery (7 staging / 35 production)
and the documents bucket keeps non-current object versions for the same window
(ADR-0007 / ADR-0014): one retention window for a full restore.

1. `aws rds restore-db-instance-to-point-in-time --source-db-instance-identifier zentax-<env> --target-db-instance-identifier zentax-<env>-restore --restore-time <ISO-8601> --db-subnet-group-name <the Data stack's subnet group> --vpc-security-group-ids <db security group id>` (the `db` group is in the Network stack; `DbEndpoint` and the subnet group are visible in the Data stack's resources) and `aws rds wait db-instance-available`.
2. Verify on the side instance. The master secret still matches (a restore carries the master password); `zentax_app` and its password exist inside the restored data.
3. Cut over by pointing the api at it: the cleanest path is renaming — `modify-db-instance --new-db-instance-identifier` the current instance to `zentax-<env>-old` and the restore to `zentax-<env>`, so `DB_HOST` (the instance endpoint address, derived from the identifier) is unchanged for the next `ZenTax-<Env>-Api` deploy. Delete `zentax-<env>-old` when done (it is billed hourly).
4. Documents: `aws s3api list-object-versions` on the affected prefix and copy the wanted version back over the current key (versioning never deletes the older version during the window).

## Destroying staging

Order matters across the two apps: the UI stacks import this app's exports, so
they must go first.

```bash
# 1. in zentax-ui/infra:  npx cdk destroy --context env=staging ZenTax-Staging-Edge ZenTax-Staging-Web
# 2. here:
npx cdk destroy --context env=staging ZenTax-Staging-Api ZenTax-Staging-Cluster ZenTax-Staging-Data ZenTax-Staging-Network
```

Staging resources carry `DESTROY` removal policies (the documents bucket
auto-empties, ECR repos empty on delete, RDS has no deletion protection, is
deleted without a final snapshot and its automated backups are deleted).
Secrets enter the 30-day recovery window;
`aws secretsmanager delete-secret --force-delete-without-recovery` if the same
names must be recreated sooner. The `RDSOSMetrics` log group goes with the
owning environment's Data stack (enhanced monitoring recreates it, without a
retention, on the next write).

## Decommissioning production (snapshot and delete — never a flag flip)

Production is `RETAIN` everywhere that holds data, the RDS instance carries
`DeletionPolicy: Snapshot` and deletion protection. There is deliberately no
setting that makes `cdk destroy` erase production; the procedure is manual and
leaves a restorable copy at every step:

1. **Snapshot first, independently of CloudFormation:**
   `aws rds create-db-snapshot --db-instance-identifier zentax-production --db-snapshot-identifier zentax-production-final-$(date +%Y%m%d)`
   and `aws rds wait db-snapshot-available ...`. Export the documents bucket if
   the data must outlive the account (`aws s3 sync s3://zentax-production-documents-... <archive>`).
2. **Lift the API-level guards only now:** set `deletionProtection: false` for
   production in `cdk.json` (here for RDS, and in the UI app for the ALB) and
   deploy `ZenTax-Production-Data` (and the UI app's `ZenTax-Production-Web`).
   The removal policies are unchanged by this: RDS is still `Snapshot`,
   everything else `Retain`.
3. Destroy the UI stacks first (`ZenTax-Production-Edge`, `ZenTax-Production-Web`,
   from `zentax-ui/infra`), then here
   `cdk destroy --context env=production ZenTax-Production-Api ZenTax-Production-Cluster ZenTax-Production-Data ZenTax-Production-Network`.
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
  `/zentax/<env>/cdk/cloudfront-prefix-list-lookup`); set
  `environments.<env>.cloudFrontPrefixListId` to skip it. It stays here, with
  the ALB security group it feeds, even though the ALB itself is the UI app's.
- The RDS connection/memory alarm thresholds come from a table of instance-class
  RAM in `lib/alarms.ts`; a class missing from it fails synth on purpose.
- `cdk.context.json` is intentionally absent: AZs are configured, so synth performs
  no lookups. If one ever appears, something started needing AWS at synth time.
- The built-in CloudFormation validator prints `W3010` ("avoid hard-coding
  availability zones") for the VPC subnets during synth/tests. It is informational
  and the direct consequence of the no-lookup choice above.
- `lib/config.ts` refuses the UI app's context keys (`webDesiredCount`,
  `originVerifyVersion`, `domainName`, `certificateArn`, `hostedZoneId`,
  `hostedZoneName`, `maxUploadBytes`) and the retired `githubRepositories`, so
  a setting that would silently do nothing here cannot linger in `cdk.json`.
