# GCP root

Cloud SQL, Cloud Run and GKE for the container + Postgres app. Driven by `make`
(see `make/gcp.mk`); plain `terraform` in this directory works too.

## Prerequisites

```bash
brew install --cask google-cloud-sdk
brew install terraform kubectl k6 cloud-sql-proxy
gcloud auth login
gcloud config set project YOUR_PROJECT_ID
gcloud auth application-default login   # Terraform and the local proxy use ADC
make gcp-config                          # sanity-check what the targets will use
```

## Layers and toggles

| Make target | Writes | Creates |
| --- | --- | --- |
| `make gcp-bootstrap` | (always on) | APIs, Artifact Registry repo |
| `make sql-create` | `db.auto.tfvars` | Cloud SQL instance, database, user, password in Secret Manager |
| `make run-deploy` | `serverless.auto.tfvars` | Cloud Run service, runtime service account, `DATABASE_URL` secret |
| `make gke-create` | `k8s.auto.tfvars` | GKE cluster, spot pool, Workload Identity binding |
| `make teardown-run` / `-gke` / `-sql` / `-all` | removes the file(s) | next apply destroys those resources |

`make tf-plan` previews any of them with the Makefile's variables.

## Files

| File | Contents |
| --- | --- |
| `versions.tf` | Terraform >= 1.5, google ~> 6.0, random ~> 3.6 |
| `variables.tf` | The shared contract plus `project_id`, `zone`, `registry_repo` |
| `locals.tf` | This app's env map and the Cloud SQL socket `DATABASE_URL` |
| `main.tf` | Provider, four module calls, the `DATABASE_URL` secret |
| `outputs.tf` | `image_base`, `db_host`, `db_password_secret`, `serverless_url`, `k8s_*` |
| `moved.tf` | State migration from the flat layout used before 2026-09 |
| `modules/registry` | 7 service APIs (`disable_on_destroy = false`), Artifact Registry |
| `modules/database` | `db-f1-micro` Postgres 17, zonal, HDD, no backups; password to Secret Manager |
| `modules/serverless` | Cloud Run v2, dedicated service account with `cloudsql.client` + `secretAccessor`, startup probe, scale 0..5 |
| `modules/kubernetes` | Zonal GKE, spot pool 1..4, `roles/cloudsql.client` for the pods' KSA via Workload Identity |

## Cost while running

| Resource | Approx. |
| --- | --- |
| Cloud SQL `db-f1-micro`, 10 GB HDD | ~$10/month |
| GKE management fee | $0.10/h (one zonal cluster is covered by the monthly free credit) |
| 2 × `e2-standard-2` spot | ~$0.04–0.05/h |
| Cloud Run at zero traffic | ~$0 (scales to zero) |
| LoadBalancer Service on GKE | ~$18/month while it exists |

## State, secrets, imports

State is local and gitignored; it holds the database password and `DATABASE_URL`.
The password lives in Secret Manager as `experiment-go-db-password`; read it with
`gcloud secrets versions access latest --secret=experiment-go-db-password`.
If a Cloud SQL user already exists in state from the old layout, the first apply updates its password in place to the generated one; nothing is recreated.

Resources created outside Terraform must be imported with their **module** addresses:

```bash
terraform import 'module.registry.google_artifact_registry_repository.containers' projects/PROJECT_ID/locations/europe-north1/repositories/containers
terraform import 'module.database[0].google_sql_database_instance.main' PROJECT_ID/experiment-go-pg
terraform import 'module.kubernetes[0].google_container_cluster.main' PROJECT_ID/europe-north1-b/experiment-go
```

## Troubleshooting

- `could not find default credentials`: `gcloud auth application-default login`.
- `The serverless service requires the database`: `create_db` is off while `image` is set. Run `make sql-create` first, or `make teardown-run` before `make teardown-sql`.
- `already exists` on apply: import it (above) or pick another `name`. Cloud SQL instance names cannot be reused for about a week after deletion.
- Cloud Run 503 right after deploy: `make run-logs`; the app fails fast on a bad config instead of serving broken.
- Provider version conflicts after a pull: `terraform init -upgrade`.
