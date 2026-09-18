# Reusable Terraform for a container + database app on GCP and AWS

Date: 2026-09-13
Status: approved design, awaiting implementation plan

## 1. Goal

Turn `deploy/terraform` into a sample infrastructure setup that:

1. Deploys **this** service (a container that needs Postgres) to Google Cloud and to AWS.
2. Can be reused for **any** other containerised application that needs a database by
   copying one root directory and editing two files.
3. Keeps the two clouds **separate**: one root module per cloud, no shared state, no
   cross-cloud Terraform dependencies. They mirror each other in structure, variable
   names and outputs so a reader can learn one and understand the other.

Each cloud offers the same three optional layers on top of an always-on base:

| Layer | GCP | AWS | Toggle |
| --- | --- | --- | --- |
| Base (always on) | 5 service APIs + Artifact Registry repo | VPC + ECR repo | none |
| Database | Cloud SQL Postgres 17 `db-f1-micro` | RDS Postgres 17 `db.t4g.micro` | `create_db` |
| Serverless containers | Cloud Run v2, scale 0..5 | ECS Fargate behind an ALB, scale 1..5 | `image != ""` |
| Kubernetes | GKE zonal, spot pool 1..4 | EKS, managed spot node group 1..4 | `create_k8s` |

## 2. Non-goals

- Remote state backends. State stays local per root. READMEs show the GCS and S3 backend
  snippets for later.
- Custom domains, TLS termination, WAF. Both serverless URLs are plain HTTP or the
  platform's default HTTPS URL.
- Multi-environment layouts (workspaces, `envs/dev`, `envs/prod`).
- CI/CD pipelines that run Terraform.
- A NAT gateway on AWS. Compute lives in public subnets on purpose (cost). The AWS README
  states this is experiment shape, not production shape.
- Changing the Go application. The infrastructure adapts to the app's existing
  environment variables (`DATABASE_URL`, `POSTGRES_*`, `MIGRATE_ON_START`, ...).

## 3. Current state (what changes)

- `deploy/terraform/` is a single flat GCP root with toggles `create_sql`, `create_gke`,
  `run_image` driven by `*.auto.tfvars` files the Makefile writes. Live state holds only
  the base layer: 5 `google_project_service` enablements and one Artifact Registry repo,
  in project `mikroteenus`. Everything else is torn down.
- Database password is a Terraform variable written into `sql.auto.tfvars` and into the
  Cloud Run `DATABASE_URL` in plain text. `make k8s-secret` reads it from `.env`.
- `deploy/k8s/` holds GKE-only manifests: Cloud SQL Auth Proxy native sidecar, Workload
  Identity ServiceAccount, `l4-rbs` LoadBalancer. Placeholders `IMAGE_PLACEHOLDER` and
  `SQL_INSTANCE_PLACEHOLDER` are filled with `sed` by the Makefile.
- `Makefile` is about 500 lines with all GCP, k8s and k6 targets inline.
- Tooling on the development machine: Terraform 1.16, gcloud authenticated to
  `mikroteenus`, kubectl with built-in kustomize, helm, Docker with buildx. **No AWS CLI and
  no AWS credentials.** The AWS root can be validated but not planned or applied here.

## 4. Layout

```
deploy/
  terraform/
    README.md                 the contract: variables, outputs, GCP/AWS mapping, reuse steps
    .gitignore                **/terraform.tfstate*  **/*.auto.tfvars  **/terraform.tfvars  **/.terraform/
    gcp/
      versions.tf             terraform >= 1.5, hashicorp/google ~> 6.0, hashicorp/random ~> 3.6
      variables.tf            the shared contract + GCP-only knobs
      locals.tf               THIS APP's env map and DSN composition (edit when reusing)
      main.tf                 provider + four module calls
      outputs.tf              the shared outputs
      moved.tf                state migration from the flat layout
      terraform.tfvars.example
      README.md
      modules/
        registry/             APIs + Artifact Registry
        database/             Cloud SQL + Secret Manager password
        serverless/           Cloud Run v2 + service account + secrets
        kubernetes/           GKE + spot pool + Workload Identity binding
    aws/
      versions.tf             terraform >= 1.5, hashicorp/aws ~> 6.0, hashicorp/random ~> 3.6
      variables.tf
      locals.tf
      main.tf
      outputs.tf
      terraform.tfvars.example
      README.md
      modules/
        network/              VPC, subnets, IGW, routes
        registry/             ECR
        database/             RDS + Secrets Manager password
        serverless/           ECS Fargate + ALB + autoscaling + secrets
        kubernetes/           EKS + spot node group + add-ons
  k8s/
    base/                     cloud-neutral manifests
    overlays/gke/             proxy sidecar, l4-rbs LoadBalancer
    overlays/eks/             RDS host + sslmode, NLB LoadBalancer
make/
  gcp.mk                      all existing GCP targets, names unchanged
  aws.mk                      aws-* targets
  k8s.mk                      kubectl targets, switched by CLOUD=gcp|aws
```

The old flat files in `deploy/terraform/*.tf` are deleted once the GCP root passes its
acceptance check. `deploy/terraform/terraform.tfstate`, its backups and
`.terraform.lock.hcl` move to `deploy/terraform/gcp/`. `deploy/terraform/.terraform/` is
deleted and re-initialised inside `gcp/`.

## 5. The shared contract

Both roots declare these variables with identical names, types and meaning. Defaults are
cloud-neutral; cloud-specific defaults are noted.

| Variable | Type | Default | Meaning |
| --- | --- | --- | --- |
| `name` | string | `"experiment-go"` | Prefix for every resource name |
| `region` | string | GCP `europe-north1`, AWS `eu-north-1` | Region for regional resources |
| `image` | string | `""` | Full image reference. Empty means no serverless service |
| `container_port` | number | `8080` | Port the container listens on |
| `health_check_path` | string | `"/healthz"` | HTTP path for platform health checks |
| `env` | map(string) | `{}` | Extra plain environment variables merged over the app defaults in `locals.tf` |
| `create_db` | bool | `false` | Create the managed Postgres |
| `db_name` | string | `"experiment"` | Database name |
| `db_user` | string | `"app"` | Application user |
| `db_engine_version` | string | `"17"` | Postgres major version |
| `create_k8s` | bool | `false` | Create the Kubernetes cluster |
| `k8s_namespace` | string | `"experiment-go"` | Namespace the app runs in (must match `deploy/k8s`) |
| `k8s_service_account` | string | `"experiment-go"` | Kubernetes ServiceAccount of the app |
| `k8s_node_count` | number | `2` | Initial nodes in the spot pool |
| `k8s_node_type` | string | GCP `e2-standard-2`, AWS `t3.medium` | Node machine type |
| `serverless_min_instances` | number | GCP `0`, AWS `1` | Fargate cannot scale to zero |
| `serverless_max_instances` | number | `5` | Upper bound |
| `serverless_cpu` | string | GCP `"1"`, AWS `"256"` | Platform-native CPU unit |
| `serverless_memory` | string | GCP `"512Mi"`, AWS `"512"` | Platform-native memory unit |

GCP-only: `project_id` (required), `zone` (default `europe-north1-b`), `registry_repo`
(default `containers`). AWS-only: `vpc_cidr` (default `10.0.0.0/16`).

Outputs, identical names on both roots:

| Output | GCP value | AWS value |
| --- | --- | --- |
| `image_base` | `REGION-docker.pkg.dev/PROJECT/containers/NAME` | `ACCOUNT.dkr.ecr.REGION.amazonaws.com/NAME` |
| `db_host` | Cloud SQL connection name `PROJECT:REGION:INSTANCE` or null | RDS endpoint hostname or null |
| `db_password_secret` | Secret Manager secret id or null | Secrets Manager secret ARN or null |
| `serverless_url` | Cloud Run URI or null | `http://ALB-DNS-NAME` or null |
| `k8s_cluster_name` | cluster name or null | cluster name or null |
| `k8s_credentials_command` | `gcloud container clusters get-credentials ...` | `aws eks update-kubeconfig ...` |

### App-specific values live in `locals.tf`

Each root has one `locals.tf` that is the only place with knowledge of this particular
application:

```hcl
locals {
  # Environment this app needs on the serverless platform. var.env overrides these.
  app_env = merge({
    APP_ENV              = "production"
    LOG_FORMAT           = "json"
    MIGRATE_ON_START     = "true"   # safe: golang-migrate takes an advisory lock
    ENABLE_BURN_ENDPOINT = "true"   # CPU sink for load tests; off for real workloads
    DB_MAX_CONNS         = "5"
  }, var.env)

  # Connection string the serverless platform hands the app as a secret.
  # GCP: unix socket mounted by Cloud Run. AWS: RDS endpoint with TLS.
  # Empty when there is no database; the serverless module's precondition then fails
  # with a clear message.
  database_url = var.create_db ? "postgres://..." : ""
}
```

Reusing for another app: copy `gcp/` or `aws/`, edit `locals.tf` (env map, DSN shape if
the app wants `POSTGRES_*` instead of `DATABASE_URL`), set `name`, `image`,
`container_port`, `health_check_path` in tfvars. Modules and variables stay untouched.

### Toggle files

The Makefile keeps toggling layers by writing small auto-loaded files into the root
directory, now with cloud-neutral names on both sides:

| File | Content | Written by |
| --- | --- | --- |
| `db.auto.tfvars` | `create_db = true` | `make sql-create` / `make aws-sql-create` |
| `serverless.auto.tfvars` | `image = "..."` | `make run-deploy` / `make aws-ecs-deploy` |
| `k8s.auto.tfvars` | `create_k8s = true` | `make gke-create` / `make aws-eks-create` |

Teardown targets delete the file and apply. No password is ever written to a tfvars file.

## 6. GCP root

### 6.1 Modules

**registry** (always on)
- `google_project_service` for `artifactregistry`, `run`, `sqladmin`, `container`,
  `cloudbuild`, and new: `secretmanager`, `iam`. `disable_on_destroy = false`.
- `google_artifact_registry_repository` Docker format. Attributes identical to today so
  the move produces no diff.
- Outputs: `repository_url`, `apis` (for `depends_on`).

**database** (`count = var.create_db ? 1 : 0`)
- `random_password` length 32, `special = false`, so the value is safe inside a URL.
- `google_sql_database_instance` `db-f1-micro`, `POSTGRES_17`, ZONAL, PD_HDD 10 GB, no
  backups, `deletion_protection = false`. Same attributes as today.
- `google_sql_database`, `google_sql_user` with `deletion_policy = "ABANDON"`, password
  from `random_password`.
- `google_secret_manager_secret` `${name}-db-password` with automatic replication, one
  `google_secret_manager_secret_version` holding the password.
- Outputs: `connection_name`, `database`, `user`, `password` (sensitive),
  `password_secret_id`.

**serverless** (`count = var.image != "" ? 1 : 0`)
- Inputs: `image`, `container_port`, `health_check_path`, `env` (map), `secret_env`
  (map of env var name to an existing Secret Manager `secret_id`),
  `cloudsql_connection_name`, `min/max_instances`, `cpu`, `memory`.
- `google_service_account` `${name}-run` with `roles/cloudsql.client` on the project.
- For each `secret_env` entry: `google_secret_manager_secret_iam_member`
  `roles/secretmanager.secretAccessor` for the service account on that secret. The module
  never holds secret values, only references. Terraform cannot iterate a map of sensitive
  values with `for_each`, so the value-bearing secret is created by the root (6.2).
- `google_cloud_run_v2_service` with `deletion_protection = false`, the service account,
  Cloud SQL volume mount at `/cloudsql`, plain `env` blocks from `env`, `env` blocks with
  `value_source.secret_key_ref` from `secret_env`, an HTTP startup probe on
  `health_check_path`, scaling from the variables.
- `google_cloud_run_v2_service_iam_member` `roles/run.invoker` for `allUsers`.
- Precondition: a `lifecycle.precondition` on the Cloud Run resource requires
  `cloudsql_connection_name != ""`. The root passes
  `var.create_db ? module.database[0].connection_name : ""`, so the error message is the
  same as today: serverless requires the database, tear down serverless first.
- Output: `url`.

**kubernetes** (`count = var.create_k8s ? 1 : 0`)
- `google_container_cluster` zonal, no default pool, `deletion_protection = false`,
  REGULAR channel, Workload Identity pool, managed Prometheus off. Same as today.
- `google_container_node_pool` spot, autoscaling 1..4, `pd-standard` 32 GB, GKE_METADATA.
- `google_project_iam_member` `roles/cloudsql.client` for the Kubernetes ServiceAccount
  principal. Uses `data.google_project.current` inside the module.
- Outputs: `cluster_name`, `zone`.

### 6.2 Root wiring

`main.tf` calls the four modules. `locals.database_url` is
`postgres://USER:PASSWORD@/DB?host=/cloudsql/CONNECTION_NAME`. The root creates one
`google_secret_manager_secret` `${name}-database-url` (count on `image != ""`) with a
version holding that value, and passes
`secret_env = { DATABASE_URL = <that secret_id> }` to `serverless`. Plain env is
`local.app_env`. Another app that wants `POSTGRES_PASSWORD` instead can pass
`module.database[0].password_secret_id` directly.

### 6.3 State migration (`moved.tf`)

```hcl
moved { from = google_project_service.apis                          to = module.registry.google_project_service.apis }
moved { from = google_artifact_registry_repository.containers       to = module.registry.google_artifact_registry_repository.containers }
moved { from = google_sql_database_instance.main[0]                 to = module.database[0].google_sql_database_instance.main }
moved { from = google_sql_database.app[0]                           to = module.database[0].google_sql_database.app }
moved { from = google_sql_user.app[0]                               to = module.database[0].google_sql_user.app }
moved { from = google_cloud_run_v2_service.api[0]                   to = module.serverless[0].google_cloud_run_v2_service.api }
moved { from = google_cloud_run_v2_service_iam_member.public[0]     to = module.serverless[0].google_cloud_run_v2_service_iam_member.public }
moved { from = google_container_cluster.main[0]                     to = module.kubernetes[0].google_container_cluster.main }
moved { from = google_container_node_pool.spot[0]                   to = module.kubernetes[0].google_container_node_pool.spot }
moved { from = google_project_iam_member.workload_identity_cloudsql[0] to = module.kubernetes[0].google_project_iam_member.workload_identity_cloudsql }
```

Steps: move state, backups and lock file into `gcp/`; `terraform init`; `terraform plan`
with the Makefile's variables. **Acceptance:** the plan lists the six live resource
instances (five API enablements and the repository) as moved, creates exactly two new
`google_project_service` enablements (`secretmanager`, `iam`), and destroys or replaces
nothing. The `google_sql_user` no longer has a
`password` from a variable; because it is not in state this causes no diff today, and the
README notes that an existing user would be updated in place on first apply.

## 7. AWS root

### 7.1 Modules

**network** (always on, free)
- `aws_vpc` `var.vpc_cidr`, DNS hostnames and support on.
- Two AZs from `data.aws_availability_zones`. Two public subnets (`/20`,
  `map_public_ip_on_launch = true`, tag `kubernetes.io/role/elb = 1`) and two private
  subnets (`/20`, no route to the internet). One internet gateway, one public route table.
- Outputs: `vpc_id`, `public_subnet_ids`, `private_subnet_ids`, `vpc_cidr`.

**registry** (always on)
- `aws_ecr_repository` `${name}`, mutable tags, scan on push, `force_delete = true` so
  teardown works with images present.
- `aws_ecr_lifecycle_policy` keeping the last 10 images.
- Output: `repository_url`.

**database** (`count = var.create_db ? 1 : 0`)
- `random_password` as on GCP.
- `aws_db_subnet_group` over the private subnets.
- `aws_security_group` `${name}-db` with no ingress rules of its own. Consumers add
  `aws_vpc_security_group_ingress_rule` resources pointing at it, which avoids a
  dependency cycle between the toggled modules.
- `aws_db_instance`: engine `postgres`, `engine_version = var.db_engine_version`,
  `db.t4g.micro`, 20 GB `gp3`, single AZ, `publicly_accessible = false`,
  `backup_retention_period = 0`, `skip_final_snapshot = true`,
  `deletion_protection = false`, `apply_immediately = true`, `db_name`, `username`,
  `password` from `random_password`. The default Postgres 17 parameter group has
  `rds.force_ssl = 1`, so clients must use `sslmode=require`.
- `aws_secretsmanager_secret` `${name}-db-password` with `recovery_window_in_days = 0`
  so teardown and re-create work, plus one version holding the password.
- Outputs: `address`, `port`, `database`, `user`, `password` (sensitive),
  `password_secret_arn`, `security_group_id`.

**serverless** (`count = var.image != "" ? 1 : 0`)
- Inputs mirror GCP plus `vpc_id`, `public_subnet_ids`, `db_security_group_id`.
- `aws_ecs_cluster` `${name}` with Container Insights off.
- `aws_cloudwatch_log_group` `/ecs/${name}`, 7 day retention.
- IAM: execution role with `AmazonECSTaskExecutionRolePolicy` plus an inline policy
  allowing `secretsmanager:GetSecretValue` on the ARNs in `secret_env`; an empty task
  role.
- `secret_env` is a map of env var name to an existing Secrets Manager secret ARN. The
  module never holds secret values (same reason as on GCP).
- `aws_ecs_task_definition`: FARGATE, `awsvpc`, `cpu = var.cpu`, `memory = var.memory`,
  `runtime_platform` `LINUX` / `X86_64` (images are built for `linux/amd64`), one
  container with `portMappings`, `environment` from `env`, `secrets` from the secret ARNs,
  `awslogs` log configuration.
- Security groups: `${name}-alb` (ingress 80 from `0.0.0.0/0`, egress all) and
  `${name}-task` (ingress `container_port` from the ALB SG, egress all).
  `aws_vpc_security_group_ingress_rule` on the database SG allowing 5432 from the task SG.
- `aws_lb` application, internet-facing, public subnets. `aws_lb_target_group` type `ip`,
  port `container_port`, health check on `health_check_path` matcher 200,
  `deregistration_delay = 10`. `aws_lb_listener` port 80 forwarding to the target group.
- `aws_ecs_service`: FARGATE, `desired_count = var.min_instances`, public subnets with
  `assign_public_ip = true`, task SG, load balancer attachment, deployment circuit breaker
  with rollback, `lifecycle { ignore_changes = [desired_count] }`.
- `aws_appautoscaling_target` (min/max) and `aws_appautoscaling_policy` target tracking on
  `ECSServiceAverageCPUUtilization` at 60 percent.
- Precondition: `image != ""` requires `create_db`.
- Output: `url` = `http://${aws_lb.dns_name}`.

**kubernetes** (`count = var.create_k8s ? 1 : 0`)
- Inputs: `name`, `public_subnet_ids`, `node_count`, `node_type`, `db_security_group_id`.
- IAM cluster role with `AmazonEKSClusterPolicy`. Node role with
  `AmazonEKSWorkerNodePolicy`, `AmazonEKS_CNI_Policy`, `AmazonEC2ContainerRegistryPullOnly`.
- `aws_eks_cluster` in the public subnets, public endpoint, `access_config` with
  `authentication_mode = "API"` and `bootstrap_cluster_creator_admin_permissions = true`.
  Kubernetes version left to the provider default (latest).
- `aws_eks_node_group` `spot-pool`, `capacity_type = "SPOT"`, `instance_types = [node_type]`,
  scaling min 1, desired `node_count`, max 4, 20 GB disk.
- `aws_eks_addon` for `vpc-cni`, `coredns`, `kube-proxy` and `metrics-server` (community
  add-on; required for the HPA). Exact add-on names and version compatibility are checked
  against the provider and EKS docs during implementation. Fallback if `metrics-server`
  is unavailable as an add-on: the README documents the `helm install` command.
- `aws_vpc_security_group_ingress_rule` on the database SG allowing 5432 from the cluster
  security group. It has `count = var.db_security_group_id != "" ? 1 : 0`, because the
  cluster may exist without the database. The root passes `""` when `create_db` is false.
- Outputs: `cluster_name`, `cluster_arn`.

### 7.2 Root wiring

`locals.database_url` is
`postgres://USER:PASSWORD@ADDRESS:5432/DB?sslmode=require`. The root creates one
`aws_secretsmanager_secret` `${name}-database-url` (count on `image != ""`,
`recovery_window_in_days = 0`) with a version holding that value, and passes
`secret_env = { DATABASE_URL = <that ARN> }` to `serverless`. Plain env is
`local.app_env`. `network` and `registry` are always called; the other three carry
`count`.

### 7.3 Kubeconfig context

`aws eks update-kubeconfig --name NAME --region REGION` creates a context named by the
cluster ARN `arn:aws:eks:REGION:ACCOUNT:cluster/NAME`. The Makefile derives this string
and passes it as `--context`, see section 9.

## 8. Kubernetes manifests (kustomize)

```
deploy/k8s/
  base/
    kustomization.yaml       lists the files below
    namespace.yaml           unchanged
    serviceaccount.yaml      unchanged (no annotations needed on either cloud)
    configmap.yaml           APP_ENV, HTTP_PORT, LOG_*, POSTGRES_PORT/USER/DB, MIGRATE_ON_START=false,
                             DB_MAX_CONNS=5, DB_MIN_CONNS=1, ENABLE_BURN_ENDPOINT=true.
                             POSTGRES_HOST and POSTGRES_SSLMODE are set by overlays.
    deployment.yaml          api container only, probes, preStop sleep, no CPU limit, IMAGE_PLACEHOLDER
    service.yaml             ClusterIP only
    hpa.yaml                 unchanged
    pdb.yaml                 unchanged
    migrate-job.yaml         migrate container only, IMAGE_PLACEHOLDER
  overlays/gke/
    kustomization.yaml       resources: ../../base, lb-service.yaml; patches below
    configmap-patch.yaml     POSTGRES_HOST=127.0.0.1, POSTGRES_SSLMODE=disable
    proxy-sidecar-deployment.yaml   strategic merge: initContainers cloud-sql-proxy (SQL_INSTANCE_PLACEHOLDER)
    proxy-sidecar-job.yaml          same for the Job
    lb-service.yaml          type LoadBalancer, annotation cloud.google.com/l4-rbs, externalTrafficPolicy Local
  overlays/eks/
    kustomization.yaml
    configmap-patch.yaml     POSTGRES_HOST=RDS_HOST_PLACEHOLDER, POSTGRES_SSLMODE=require
    lb-service.yaml          type LoadBalancer, annotation service.beta.kubernetes.io/aws-load-balancer-type: nlb
```

Render pipeline stays `kubectl kustomize OVERLAY | sed PLACEHOLDERS | kubectl apply -f -`.
No kustomize `images:` transformer, so the Makefile keeps one mechanism for all
placeholders. **Acceptance:** the rendered `gke` overlay is semantically identical to
today's `deploy/k8s/*.yaml` (same resources, same fields; ordering may differ). The
rendered `eks` overlay builds and passes `kubectl apply --dry-run=client --validate=false`.

The old flat `deploy/k8s/0*.yaml` files are deleted after the equivalence check.

## 9. Makefile

The `Makefile` keeps the local, quality, build, docker, compose, migration, smoke and k6
sections and ends with `include make/gcp.mk make/aws.mk make/k8s.mk`. `make help` already
greps `$(MAKEFILE_LIST)`, so included targets appear automatically.

### make/gcp.mk

All current GCP targets with their current names. Changes:

- `TF_DIR := deploy/terraform/gcp`. `TF_VARS` passes `project_id`, `region`, `zone`,
  `name`, `registry_repo`, `db_name`, `db_user`, `k8s_namespace`, `k8s_service_account`,
  `k8s_node_count` and `k8s_node_type` as `-var` flags. The three toggles come only from
  the auto.tfvars files.
- `sql-create` writes `db.auto.tfvars` with only `create_db = true`. No password check.
- `run-deploy` writes `serverless.auto.tfvars` with `image = "..."`.
- `gke-create` writes `k8s.auto.tfvars` with `create_k8s = true`.
- `sql-proxy` and `sql-psql` print or use the password fetched with
  `gcloud secrets versions access latest --secret=$(APP_NAME)-db-password`.
- `teardown-*` remove the new file names.
- `SQL_PASSWORD` disappears from the GCP flow.

### make/aws.mk

Variables: `AWS_REGION ?= eu-north-1`, `AWS_ACCOUNT_ID` (lazy `aws sts get-caller-identity`),
`AWS_TF_DIR := deploy/terraform/aws`, `AWS_TF_VARS` mirroring `TF_VARS`,
`AWS_IMAGE_BASE = $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com/$(APP_NAME)`,
`AWS_IMAGE ?= $(AWS_IMAGE_BASE):$(VERSION)`, `RDS_INSTANCE`, `ECS_SERVICE`, `EKS_CLUSTER`.

| Target | Does |
| --- | --- |
| `aws-config` | Print resolved account, region, image, cluster names |
| `aws-bootstrap` | `terraform init` + apply base (VPC + ECR), `aws ecr get-login-password \| docker login` |
| `aws-tf-plan` | Plan with the Makefile's variables |
| `aws-image-push` | `docker buildx --platform linux/amd64 --push` to ECR |
| `aws-sql-create` | Write `db.auto.tfvars`, apply |
| `aws-sql-info` | `aws rds describe-db-instances` summary |
| `aws-ecs-deploy` | Write `serverless.auto.tfvars` with `AWS_IMAGE`, apply, wait for service stable |
| `aws-ecs-url` | `terraform output -raw serverless_url` |
| `aws-ecs-smoke` | k6 smoke against that URL |
| `aws-ecs-logs` | `aws logs tail /ecs/$(APP_NAME) --follow` |
| `aws-eks-create` | Write `k8s.auto.tfvars`, apply, then `aws-eks-creds` |
| `aws-eks-creds` | `aws eks update-kubeconfig` |
| `aws-teardown-ecs` / `-eks` / `-sql` | Remove the file, apply |
| `aws-teardown-all` | `make k8s-delete CLOUD=aws` (ignore errors), remove all three files, apply |

### make/k8s.mk

One set of kubectl targets, parameterised by `CLOUD ?= gcp`:

| Variable | `CLOUD=gcp` | `CLOUD=aws` |
| --- | --- | --- |
| `K8S_OVERLAY` | `deploy/k8s/overlays/gke` | `deploy/k8s/overlays/eks` |
| `K8S_CONTEXT` | `gke_$(PROJECT_ID)_$(ZONE)_$(GKE_CLUSTER)` | `arn:aws:eks:$(AWS_REGION):$(AWS_ACCOUNT_ID):cluster/$(EKS_CLUSTER)` |
| `K8S_RENDER` | sed `IMAGE_PLACEHOLDER` to `$(IMAGE)`, `SQL_INSTANCE_PLACEHOLDER` to `$(SQL_CONN)` | sed `IMAGE_PLACEHOLDER` to `$(AWS_IMAGE)`, `RDS_HOST_PLACEHOLDER` to `terraform output -raw db_host` |
| `K8S_DB_SECRET_CMD` | `gcloud secrets versions access latest --secret=...` | `aws secretsmanager get-secret-value --query SecretString --output text` |

`KUBECTL := kubectl --context=$(K8S_CONTEXT)` is used by every target, so a stale current
context can never receive the wrong manifests. The `aws` values that shell out
(`AWS_ACCOUNT_ID`, the RDS host) are defined with lazy `=` assignment, so `make` targets
for `gcp` never call the AWS CLI. Targets keep their names: `k8s-secret`,
`k8s-render`, `k8s-deploy`, `k8s-migrate`, `k8s-status`, `k8s-logs`, `k8s-url`,
`k8s-forward`, `k8s-scale`, `k8s-watch`, `k8s-top`, `k8s-delete`, `loadtest-cluster`.

Behaviour changes:
- `k8s-secret` creates the namespace and the `experiment-go-db` secret from
  `K8S_DB_SECRET_CMD`, no longer from `.env`.
- `k8s-deploy` deletes a stale migrate Job, renders and applies the whole overlay, waits
  for the Job to complete, then waits for the rollout. Deploy and migration are one step.
- `k8s-migrate` deletes the Job, re-applies the overlay (idempotent for everything else),
  waits, prints the Job log.
- `k8s-url` for `aws` reads the NLB hostname (`.status.loadBalancer.ingress[0].hostname`)
  instead of `.ip`.

Usage: `make gke-create k8s-deploy` as today; `make aws-eks-create k8s-deploy CLOUD=aws`.

## 10. Documentation

| File | Change |
| --- | --- |
| `deploy/terraform/README.md` | New. The contract tables from section 5, the layer mapping from section 1, "reuse for another app" steps, backend snippets, why two roots |
| `deploy/terraform/gcp/README.md` | Adapted from today's `deploy/terraform/README.md`: toggles, ordering, files, state and secrets (password now in Secret Manager), import addresses updated to module paths, troubleshooting |
| `deploy/terraform/aws/README.md` | New. Prerequisites (`aws` CLI, credentials, `aws configure`), cost table, phases mirroring the GCP walkthrough, ordering constraints, gotchas (no NAT, Fargate min 1, `sslmode=require`, NLB via legacy provider, spot interruptions), troubleshooting |
| `deploy/README.md` | Add an AWS walkthrough section mirroring phases 1..6, `CLOUD=aws` usage, updated teardown |
| `README.md` | Cloud section mentions both clouds, kustomize overlays, new make targets |
| `AGENTS.md` | Directory table, commands, deployment state, gotchas updated |

## 11. Verification and acceptance

Run on this machine before declaring done:

1. `terraform fmt -check -recursive deploy/terraform` passes.
2. `terraform init` and `terraform validate` pass in `gcp/`, `aws/` and every module.
3. GCP: `make tf-plan` shows the six live resource instances moved, two new API
   enablements created, nothing destroyed or replaced.
4. AWS: `terraform validate` only. `terraform plan` is impossible without credentials; the
   report states this and lists what the first `make aws-bootstrap` on a configured
   machine is expected to create.
5. `kubectl kustomize deploy/k8s/overlays/gke` and `.../eks` build. Rendered output passes
   `kubectl apply --dry-run=client --validate=false -f -`. GKE render is semantically
   identical to the previous flat manifests.
6. `make -n` for every new or changed target expands without errors for both `CLOUD`
   values (with dummy `AWS_ACCOUNT_ID` for aws).
7. `make help` lists targets from all three include files.
8. `make check` still passes (Go code untouched).
9. Old flat Terraform files and old flat k8s manifests are removed; no references remain
   (`grep` for `deploy/k8s/0`, `sql.auto.tfvars`, `run.auto.tfvars`, `gke.auto.tfvars`,
   `create_sql`, `run_image`, `create_gke`, `SQL_PASSWORD`).

## 12. Risks and notes

- **AWS unverified by apply.** Provider attribute names, add-on names and IAM policy
  names are checked against current provider docs during implementation, but the first
  real apply may surface issues. The AWS README opens with this caveat.
- **EKS LoadBalancer Services** rely on the EKS-managed cloud controller creating an NLB
  from the annotation. If that stops working, `make k8s-forward CLOUD=aws` is the fallback.
- **Public subnets without NAT** expose node and task public IPs. Security groups admit
  only ALB traffic to tasks and nothing inbound to nodes beyond the cluster SG defaults.
- **Fargate minimum of one task** means the ECS layer costs money while it exists. Cost
  table says so.
- **Moves.** Any attribute change on the two live resources during the refactor would show
  as an update; the acceptance check in section 11 catches it.
- **Secret hook on this machine.** Shell commands whose text contains `PASSWORD`, `SECRET`
  or `.env` are blocked. Files containing such words are written with the editor tools,
  not shell heredocs.
- **No git repository.** Nothing is committed; the spec and code exist only on disk.
