# AWS root

RDS, ECS Fargate and EKS for the container + Postgres app. Driven by `make`
(see `make/aws.mk`); plain `terraform` in this directory works too.

> This root was written and validated (`terraform validate`) without AWS credentials.
> The first `make aws-bootstrap` on a configured machine is the first real apply; expect
> to fix small things (IAM policy names, add-on versions) and please update this README.

## Prerequisites

```bash
brew install awscli terraform kubectl k6
aws configure                     # or export AWS_PROFILE=... for SSO
aws sts get-caller-identity       # must print your account
make aws-config                   # sanity-check account, region, image, names
```

Region defaults to `eu-north-1` (Stockholm). Override with `AWS_REGION=... make ...` or in `.env`.

## Layers and toggles

| Make target | Writes | Creates |
| --- | --- | --- |
| `make aws-bootstrap` | (always on) | VPC with 2 public + 2 private subnets, ECR repo; logs docker in to ECR |
| `make aws-image-push` | — | `linux/amd64` image in ECR |
| `make aws-sql-create` | `db.auto.tfvars` | RDS Postgres 17 `db.t4g.micro`, password in Secrets Manager (~10 min) |
| `make aws-ecs-deploy` | `serverless.auto.tfvars` | ECS cluster, Fargate service 1..5 behind an ALB, `DATABASE_URL` secret |
| `make aws-eks-create` | `k8s.auto.tfvars` | EKS cluster, spot node group 1..4, vpc-cni/kube-proxy/coredns/metrics-server (~15 min) |
| `make k8s-deploy CLOUD=aws` | — | Namespace, secret, app manifests via the `eks` overlay |
| `make aws-teardown-ecs` / `-eks` / `-sql` / `-all` | removes the file(s) | next apply destroys those resources |

`make aws-tf-plan` previews any of them.

## Files

| File | Contents |
| --- | --- |
| `versions.tf` | Terraform >= 1.5, aws ~> 6.0, random ~> 3.6 |
| `variables.tf` | The shared contract plus `vpc_cidr` |
| `locals.tf` | This app's env map and the RDS `DATABASE_URL` (`sslmode=require`) |
| `main.tf` | Provider with default tags, five module calls, the `DATABASE_URL` secret |
| `outputs.tf` | `image_base`, `db_host`, `db_password_secret`, `serverless_url`, `k8s_*` |
| `modules/network` | VPC, 2 AZ, public subnets (compute) + private subnets (RDS), IGW, no NAT |
| `modules/registry` | ECR repo, scan on push, keep last 10 images, `force_delete` |
| `modules/database` | RDS Postgres, 20 GB gp3, single AZ, no backups; SG with ingress added by consumers |
| `modules/serverless` | ECS cluster, Fargate task (0.25 vCPU / 512 MB, x86_64), ALB + target group, CPU target tracking 60 % |
| `modules/kubernetes` | EKS (API auth mode, creator is admin), managed SPOT node group, add-ons |

## Cost while running

| Resource | Approx. |
| --- | --- |
| RDS `db.t4g.micro`, 20 GB gp3 | ~$15/month |
| ALB | ~$18/month + LCU |
| Fargate 0.25 vCPU / 0.5 GB, 1 task | ~$9/month (cannot scale to zero) |
| EKS control plane | $0.10/h |
| 2 × `t3.medium` spot | ~$0.02–0.03/h |
| NLB from the `experiment-go-lb` Service | ~$18/month while it exists |
| VPC, subnets, IGW, ECR | free / cents |

## Design notes and gotchas

- **No NAT gateway.** Tasks and nodes sit in public subnets with public IPs and pull
  images through the internet gateway. Security groups admit only ALB traffic to tasks
  and nothing new to nodes. RDS is in private subnets and never public.
- **`sslmode=require`.** RDS Postgres 15+ forces TLS; the k8s `eks` overlay and the
  `DATABASE_URL` secret both set it.
- **Fargate minimum is one task**, so the ECS layer costs money while it exists.
- **EKS LoadBalancer Services** get an NLB from the EKS cloud controller via the
  annotation in `deploy/k8s/overlays/eks/lb-service.yaml`. If that stops working,
  `make k8s-forward CLOUD=aws`.
- **Spot nodes** can be reclaimed with two minutes notice. The PDB keeps one pod alive.
- **Connection budget.** `db.t4g.micro` allows roughly 80 connections; `DB_MAX_CONNS ×
  replicas` must stay below it.
- **psql access.** RDS is private. Use `kubectl -n experiment-go run psql --rm -it
  --image=postgres:17-alpine -- psql "postgres://app:PASSWORD@HOST:5432/experiment?sslmode=require"`
  from the EKS cluster, with the password from
  `aws secretsmanager get-secret-value --secret-id experiment-go-db-password --query SecretString --output text`.

## Troubleshooting

- `Unable to locate credentials`: `aws configure` or `export AWS_PROFILE=...`.
- `The serverless service requires the database`: run `make aws-sql-create` first, or `make aws-teardown-ecs` before `make aws-teardown-sql`.
- `metrics-server` add-on unavailable for the cluster version: remove it from `modules/kubernetes/main.tf` and run `helm install metrics-server metrics-server/metrics-server -n kube-system` after `make aws-eks-creds`.
- ECS tasks stop with `CannotPullContainerError`: the image is not in ECR for this account/region (`make aws-image-push`) or the tasks have no public IP.
- Pods `CrashLoopBackOff` with `DATABASE_URL (or POSTGRES_*) must be set`: `make k8s-deploy CLOUD=aws` did not render `RDS_HOST_PLACEHOLDER`; check `make -s k8s-render CLOUD=aws | grep POSTGRES_HOST` and that `terraform output` shows `db_host`.
