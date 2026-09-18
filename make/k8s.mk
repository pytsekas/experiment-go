## ----------------------------------------------------------------- kubernetes
## kubectl targets shared by GKE and EKS. CLOUD=gcp (default) or CLOUD=aws picks the
## kustomize overlay, the placeholders, where the DB password comes from, and an
## explicit kubeconfig context so a stale current-context can never receive the
## wrong manifests. Example: make aws-eks-create k8s-deploy CLOUD=aws

CLOUD  ?= gcp
K8S_NS ?= experiment-go
K8S_SA ?= experiment-go

ifeq ($(CLOUD),gcp)
K8S_OVERLAY        := deploy/k8s/overlays/gke
K8S_CONTEXT         = gke_$(PROJECT_ID)_$(ZONE)_$(GKE_CLUSTER)
K8S_IMAGE           = $(IMAGE)
K8S_RENDER          = sed -e 's|IMAGE_PLACEHOLDER|$(K8S_IMAGE)|g' -e 's|SQL_INSTANCE_PLACEHOLDER|$(SQL_CONN)|g'
K8S_DB_PASSWORD_CMD = $(SQL_PASSWORD_CMD)
K8S_LB_FIELD       := ip
else ifeq ($(CLOUD),aws)
K8S_OVERLAY        := deploy/k8s/overlays/eks
K8S_CONTEXT         = arn:aws:eks:$(AWS_REGION):$(AWS_ACCOUNT_ID):cluster/$(EKS_CLUSTER)
K8S_IMAGE           = $(AWS_IMAGE)
K8S_RENDER          = sed -e 's|IMAGE_PLACEHOLDER|$(K8S_IMAGE)|g' -e 's|RDS_HOST_PLACEHOLDER|$(RDS_HOST)|g'
K8S_DB_PASSWORD_CMD = $(AWS_DB_PASSWORD_CMD)
K8S_LB_FIELD       := hostname
else
$(error CLOUD must be gcp or aws, got "$(CLOUD)")
endif

KUBECTL = kubectl --context=$(K8S_CONTEXT)

# Rendered manifests for the selected cloud. Used inline by the apply targets so
# that `make -n` stays a true dry run (a line containing $(MAKE) always executes).
K8S_MANIFESTS = kubectl kustomize $(K8S_OVERLAY) | $(K8S_RENDER)

# Same reason as in aws.mk: these expand to shell calls under CLOUD=aws and the
# recipes reference them explicitly, so nothing needs them in the environment.
unexport K8S_CONTEXT KUBECTL K8S_RENDER K8S_MANIFESTS

.PHONY: k8s-secret
k8s-secret: ## Create/update the database password secret from the cloud secret store
	@$(KUBECTL) create namespace $(K8S_NS) --dry-run=client -o yaml | $(KUBECTL) apply -f -
	@$(KUBECTL) -n $(K8S_NS) create secret generic $(APP_NAME)-db \
		--from-literal=password="$$($(K8S_DB_PASSWORD_CMD))" \
		--dry-run=client -o yaml | $(KUBECTL) apply -f -

.PHONY: k8s-render
k8s-render: ## Print the manifests with image/database filled in (no apply)
	@$(K8S_MANIFESTS)

.PHONY: k8s-deploy
k8s-deploy: k8s-secret ## Apply the overlay: runs the migration Job, then rolls the Deployment
	$(KUBECTL) -n $(K8S_NS) delete job $(APP_NAME)-migrate --ignore-not-found
	@$(K8S_MANIFESTS) | $(KUBECTL) apply -f -
	$(KUBECTL) -n $(K8S_NS) wait --for=condition=complete job/$(APP_NAME)-migrate --timeout=180s
	$(KUBECTL) -n $(K8S_NS) rollout status deploy/$(APP_NAME) --timeout=180s

.PHONY: k8s-migrate
k8s-migrate: ## Re-run only the migration Job (delete + recreate, then show its log)
	$(KUBECTL) -n $(K8S_NS) delete job $(APP_NAME)-migrate --ignore-not-found
	@$(K8S_MANIFESTS) | $(KUBECTL) apply -f -
	$(KUBECTL) -n $(K8S_NS) wait --for=condition=complete job/$(APP_NAME)-migrate --timeout=180s
	$(KUBECTL) -n $(K8S_NS) logs job/$(APP_NAME)-migrate -c migrate

.PHONY: k8s-status
k8s-status: ## Pods, HPA, services and recent events
	$(KUBECTL) -n $(K8S_NS) get pods,hpa,svc
	@echo
	$(KUBECTL) -n $(K8S_NS) get events --sort-by=.lastTimestamp | tail -15

.PHONY: k8s-logs
k8s-logs: ## Tail API logs from all pods
	$(KUBECTL) -n $(K8S_NS) logs -l app.kubernetes.io/component=api -c api -f --max-log-requests=10

.PHONY: k8s-url
k8s-url: ## Print the load balancer URL (empty until assigned)
	@addr=$$($(KUBECTL) -n $(K8S_NS) get svc $(APP_NAME)-lb -o jsonpath='{.status.loadBalancer.ingress[0].$(K8S_LB_FIELD)}'); \
	test -n "$$addr" && echo "http://$$addr" || echo "load balancer address not assigned yet, retry in a minute"

.PHONY: k8s-forward
k8s-forward: ## Port-forward the service to localhost:8080 (free alternative to the LB)
	$(KUBECTL) -n $(K8S_NS) port-forward svc/$(APP_NAME) 8080:80

.PHONY: k8s-scale
k8s-scale: ## Scale manually: make k8s-scale n=5 (pauses HPA effects)
	$(KUBECTL) -n $(K8S_NS) scale deploy/$(APP_NAME) --replicas=$(or $(n),3)

.PHONY: k8s-watch
k8s-watch: ## Watch pods and HPA react during a load test
	$(KUBECTL) -n $(K8S_NS) get hpa,pods -w

.PHONY: k8s-top
k8s-top: ## Per-pod CPU/memory usage
	$(KUBECTL) -n $(K8S_NS) top pods

.PHONY: k8s-delete
k8s-delete: ## Delete the app from the cluster (keeps the cluster)
	$(KUBECTL) delete namespace $(K8S_NS) --ignore-not-found

.PHONY: loadtest-cluster
loadtest-cluster: ## Run k6 inside the cluster: make loadtest-cluster script=load.js
	$(KUBECTL) -n $(K8S_NS) create configmap k6-scripts --from-file=loadtest/k6/ \
		--dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) -n $(K8S_NS) delete job k6 --ignore-not-found
	@sed -e 's|SCRIPT_PLACEHOLDER|$(or $(script),load.js)|' \
		-e 's|RATE_PLACEHOLDER|$(or $(RATE),50)|' \
		-e 's|DURATION_PLACEHOLDER|$(or $(DURATION),2m)|' \
		loadtest/k6/job.yaml | $(KUBECTL) apply -f -
	$(KUBECTL) -n $(K8S_NS) wait --for=condition=ready pod -l app.kubernetes.io/name=k6 --timeout=120s || true
	$(KUBECTL) -n $(K8S_NS) logs -f job/k6
