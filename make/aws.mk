## ------------------------------------------------------------------------- aws
## Needs the aws CLI with credentials (aws configure, or AWS_PROFILE / SSO).
## Infrastructure is Terraform-managed (deploy/terraform/aws); same wrapper shape as
## the GCP targets: toggle files + terraform apply. Kubernetes operations are the
## shared k8s-* targets with CLOUD=aws.

AWS_REGION      ?= eu-north-1
AWS_TF_DIR      := deploy/terraform/aws
AWS_TF          := terraform -chdir=$(AWS_TF_DIR)
AWS_TF_VARS      = -var=region=$(AWS_REGION) -var=name=$(APP_NAME) \
	-var=db_name=$(SQL_DB) -var=db_user=$(SQL_USER) \
	-var=k8s_namespace=$(K8S_NS) -var=k8s_service_account=$(K8S_SA) \
	-var=k8s_node_count=$(EKS_NODES) -var=k8s_node_type=$(EKS_MACHINE)
AWS_TF_APPLY     = $(AWS_TF) apply -input=false -auto-approve $(AWS_TF_VARS)

AWS_ACCOUNT_ID   = $(shell aws sts get-caller-identity --query Account --output text 2>/dev/null)
RDS_HOST         = $(shell $(AWS_TF) output -raw db_host 2>/dev/null)

ECR_REGISTRY     = $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
AWS_IMAGE_BASE   = $(ECR_REGISTRY)/$(APP_NAME)
AWS_IMAGE       ?= $(AWS_IMAGE_BASE):$(VERSION)

# Resolved lazily and only by the aws-* / CLOUD=aws targets that use them. The
# Makefile has a global `export`, so every variable that expands to a shell call
# (directly or through a derived variable) is unexported; otherwise make would run
# the aws CLI for every recipe line of every target, including `make test`.
unexport AWS_ACCOUNT_ID RDS_HOST ECR_REGISTRY AWS_IMAGE_BASE AWS_IMAGE

RDS_INSTANCE     = $(APP_NAME)-pg
AWS_DB_PASSWORD_CMD = aws secretsmanager get-secret-value --region $(AWS_REGION) --secret-id $(APP_NAME)-db-password --query SecretString --output text

ECS_CLUSTER      = $(APP_NAME)
ECS_SERVICE      = $(APP_NAME)
EKS_CLUSTER      = $(APP_NAME)
EKS_NODES       ?= 2
EKS_MACHINE     ?= t3.medium

.PHONY: aws-config
aws-config: ## Show the resolved AWS settings (run this first)
	@echo "AWS_ACCOUNT_ID = $(AWS_ACCOUNT_ID)"
	@echo "AWS_REGION     = $(AWS_REGION)"
	@echo "AWS_IMAGE      = $(AWS_IMAGE)"
	@echo "RDS_INSTANCE   = $(RDS_INSTANCE)"
	@echo "EKS_CLUSTER    = $(EKS_CLUSTER) ($(EKS_NODES)x $(EKS_MACHINE) spot)"

.PHONY: aws-bootstrap
aws-bootstrap: ## Terraform: VPC + ECR repo, then docker login to ECR
	$(AWS_TF) init -input=false
	$(AWS_TF_APPLY)
	aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_REGISTRY)

.PHONY: aws-tf-plan
aws-tf-plan: ## Show what Terraform would change on AWS, with the Makefile's variables
	$(AWS_TF) plan $(AWS_TF_VARS)

.PHONY: aws-image-push
aws-image-push: ## Build for linux/amd64 and push to ECR
	docker buildx build --platform linux/amd64 \
		--build-arg VERSION=$(VERSION) \
		-t $(AWS_IMAGE) -t $(AWS_IMAGE_BASE):latest --push .
	@echo "pushed $(AWS_IMAGE)"

## -------------------------------------------------------------------------- rds

.PHONY: aws-sql-create
aws-sql-create: ## Terraform: create the cheapest RDS Postgres instance (~10 minutes)
	@echo 'create_db = true' > $(AWS_TF_DIR)/db.auto.tfvars
	$(AWS_TF_APPLY)

.PHONY: aws-sql-info
aws-sql-info: ## Show the RDS instance state and endpoint
	aws rds describe-db-instances --region $(AWS_REGION) --db-instance-identifier $(RDS_INSTANCE) \
		--query 'DBInstances[0].{status:DBInstanceStatus,engine:EngineVersion,class:DBInstanceClass,endpoint:Endpoint.Address}' --output table

## -------------------------------------------------------------------------- ecs

.PHONY: aws-ecs-deploy
aws-ecs-deploy: ## Terraform: deploy the image to ECS Fargate behind an ALB
	@printf 'image = "%s"\n' '$(AWS_IMAGE)' > $(AWS_TF_DIR)/serverless.auto.tfvars
	$(AWS_TF_APPLY)
	aws ecs wait services-stable --region $(AWS_REGION) --cluster $(ECS_CLUSTER) --services $(ECS_SERVICE)

.PHONY: aws-ecs-url
aws-ecs-url: ## Print the ALB URL
	@$(AWS_TF) output -raw serverless_url

.PHONY: aws-ecs-smoke
aws-ecs-smoke: ## Smoke test the ECS deployment with k6
	$(K6) run -e BASE_URL=$$($(AWS_TF) output -raw serverless_url) loadtest/k6/smoke.js

.PHONY: aws-ecs-logs
aws-ecs-logs: ## Tail ECS task logs
	aws logs tail /ecs/$(APP_NAME) --region $(AWS_REGION) --follow

## -------------------------------------------------------------------------- eks

.PHONY: aws-eks-create
aws-eks-create: ## Terraform: EKS cluster with a spot node group and metrics-server (~15 minutes)
	@echo 'create_k8s = true' > $(AWS_TF_DIR)/k8s.auto.tfvars
	$(AWS_TF_APPLY)
	$(MAKE) aws-eks-creds

.PHONY: aws-eks-creds
aws-eks-creds: ## Fetch kubectl credentials for the EKS cluster
	aws eks update-kubeconfig --region $(AWS_REGION) --name $(EKS_CLUSTER)

## ---------------------------------------------------------------------- cleanup

.PHONY: aws-teardown-ecs
aws-teardown-ecs: ## Terraform: delete the ECS service, ALB and cluster
	rm -f $(AWS_TF_DIR)/serverless.auto.tfvars
	$(AWS_TF_APPLY)

.PHONY: aws-teardown-eks
aws-teardown-eks: ## Terraform: delete the EKS cluster (stops the $0.10/h fee)
	rm -f $(AWS_TF_DIR)/k8s.auto.tfvars
	$(AWS_TF_APPLY)

.PHONY: aws-teardown-sql
aws-teardown-sql: ## Terraform: delete the RDS instance (irreversible; aws-teardown-ecs first)
	rm -f $(AWS_TF_DIR)/db.auto.tfvars
	$(AWS_TF_APPLY)

.PHONY: aws-teardown-all
aws-teardown-all: ## Delete everything billable on AWS (one terraform apply)
	-$(MAKE) k8s-delete CLOUD=aws
	rm -f $(AWS_TF_DIR)/serverless.auto.tfvars $(AWS_TF_DIR)/k8s.auto.tfvars $(AWS_TF_DIR)/db.auto.tfvars
	$(AWS_TF_APPLY)
	@echo "left in place: VPC (free) + ECR images (cents/month)"
	@echo "  full wipe: $(AWS_TF) destroy $(AWS_TF_VARS)"
