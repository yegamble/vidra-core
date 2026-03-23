# Vidra Core Terraform Makefile
# Convenient commands for common infrastructure operations

.PHONY: help bootstrap init plan apply destroy fmt validate clean status outputs

# Environment can be overridden: make apply ENV=staging
ENV ?= production
REGION ?= us-east-1

help: ## Show this help message
	@echo 'Usage: make [target] ENV=<environment> REGION=<region>'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
	@echo ''
	@echo 'Environments: production, staging, dev'
	@echo 'Default ENV: production'
	@echo 'Default REGION: us-east-1'

bootstrap: ## Bootstrap Terraform backend (S3 + DynamoDB)
	@echo "Bootstrapping backend for $(ENV) in $(REGION)..."
	@./scripts/bootstrap-backend.sh $(ENV) $(REGION)

init: ## Initialize Terraform
	@echo "Initializing Terraform for $(ENV)..."
	@cd environments/$(ENV) && \
		if [ -f backend.hcl ]; then \
			terraform init -backend-config=backend.hcl; \
		else \
			echo "Error: backend.hcl not found. Run 'make bootstrap' first."; \
			exit 1; \
		fi

plan: init ## Run Terraform plan
	@echo "Planning infrastructure for $(ENV)..."
	@cd environments/$(ENV) && terraform plan -out=tfplan

apply: init ## Apply Terraform changes
	@echo "Applying infrastructure for $(ENV)..."
	@cd environments/$(ENV) && terraform apply tfplan

apply-auto-approve: init ## Apply Terraform changes without confirmation
	@echo "Applying infrastructure for $(ENV) (auto-approve)..."
	@cd environments/$(ENV) && terraform apply -auto-approve

destroy: init ## Destroy Terraform infrastructure
	@echo "WARNING: This will destroy all infrastructure for $(ENV)!"
	@read -p "Are you sure? Type 'yes' to continue: " confirm && [ "$$confirm" = "yes" ] || exit 1
	@cd environments/$(ENV) && terraform destroy

destroy-target: init ## Destroy specific resource: make destroy-target ENV=prod TARGET=module.rds
	@echo "Destroying $(TARGET) in $(ENV)..."
	@cd environments/$(ENV) && terraform destroy -target=$(TARGET)

fmt: ## Format Terraform files
	@echo "Formatting Terraform files..."
	@terraform fmt -recursive .

validate: init ## Validate Terraform configuration
	@echo "Validating Terraform configuration for $(ENV)..."
	@cd environments/$(ENV) && terraform validate

clean: ## Clean Terraform cache and plan files
	@echo "Cleaning Terraform cache..."
	@find . -type d -name ".terraform" -exec rm -rf {} + 2>/dev/null || true
	@find . -type f -name "tfplan" -delete
	@find . -type f -name ".terraform.lock.hcl" -delete
	@echo "Cleaned!"

status: init ## Show Terraform state summary
	@echo "Terraform state for $(ENV):"
	@cd environments/$(ENV) && terraform show -json | jq -r '.values.root_module.resources[] | "\(.type).\(.name)"' | sort | uniq -c

outputs: init ## Show Terraform outputs
	@echo "Outputs for $(ENV):"
	@cd environments/$(ENV) && terraform output

output-json: init ## Show Terraform outputs in JSON format
	@cd environments/$(ENV) && terraform output -json

graph: init ## Generate Terraform dependency graph
	@echo "Generating dependency graph..."
	@cd environments/$(ENV) && terraform graph | dot -Tpng > terraform-graph.png
	@echo "Graph saved to environments/$(ENV)/terraform-graph.png"

cost: init ## Estimate infrastructure cost (requires infracost)
	@echo "Estimating cost for $(ENV)..."
	@cd environments/$(ENV) && \
		if command -v infracost &> /dev/null; then \
			terraform plan -out=tfplan.binary && \
			terraform show -json tfplan.binary > tfplan.json && \
			infracost breakdown --path tfplan.json; \
		else \
			echo "Error: infracost not installed. Install from https://www.infracost.io/"; \
			exit 1; \
		fi

deploy-k8s: ## Deploy Kubernetes manifests
	@echo "Deploying Kubernetes manifests for $(ENV)..."
	@./scripts/deploy-k8s.sh $(ENV)

kubectl-config: ## Configure kubectl for EKS cluster
	@echo "Configuring kubectl for $(ENV)..."
	@cd environments/$(ENV) && \
		CLUSTER_NAME=$$(terraform output -raw eks_cluster_name) && \
		aws eks update-kubeconfig --region $(REGION) --name $$CLUSTER_NAME

get-rds-password: init ## Retrieve RDS password from Secrets Manager
	@cd environments/$(ENV) && \
		SECRET_NAME=$$(terraform output -raw rds_secret_name) && \
		aws secretsmanager get-secret-value --secret-id $$SECRET_NAME --region $(REGION) --query SecretString --output text | jq -r .password

get-redis-password: init ## Retrieve Redis password from Secrets Manager
	@cd environments/$(ENV) && \
		SECRET_NAME=$$(terraform output -raw redis_secret_name) && \
		if [ "$$SECRET_NAME" != "null" ]; then \
			aws secretsmanager get-secret-value --secret-id $$SECRET_NAME --region $(REGION) --query SecretString --output text | jq -r .auth_token; \
		else \
			echo "Redis auth not enabled"; \
		fi

state-list: init ## List all resources in Terraform state
	@cd environments/$(ENV) && terraform state list

state-show: init ## Show specific resource: make state-show ENV=prod RESOURCE=module.rds.aws_db_instance.main
	@cd environments/$(ENV) && terraform state show $(RESOURCE)

state-pull: init ## Pull current state
	@cd environments/$(ENV) && terraform state pull

state-backup: init ## Backup Terraform state
	@echo "Backing up state for $(ENV)..."
	@cd environments/$(ENV) && \
		terraform state pull > terraform.tfstate.backup.$$(date +%Y%m%d-%H%M%S)
	@echo "Backup created!"

import: init ## Import existing resource: make import ENV=prod ADDR=module.rds.aws_db_instance.main ID=vidra-prod-db
	@echo "Importing $(ID) as $(ADDR)..."
	@cd environments/$(ENV) && terraform import $(ADDR) $(ID)

taint: init ## Taint resource for recreation: make taint ENV=prod RESOURCE=module.eks.aws_eks_node_group.main[\"api\"]
	@echo "Tainting $(RESOURCE) for recreation..."
	@cd environments/$(ENV) && terraform taint $(RESOURCE)

untaint: init ## Untaint resource: make untaint ENV=prod RESOURCE=module.eks.aws_eks_node_group.main[\"api\"]
	@echo "Untainting $(RESOURCE)..."
	@cd environments/$(ENV) && terraform untaint $(RESOURCE)

refresh: init ## Refresh Terraform state
	@echo "Refreshing state for $(ENV)..."
	@cd environments/$(ENV) && terraform refresh

upgrade: ## Upgrade Terraform providers
	@echo "Upgrading providers for $(ENV)..."
	@cd environments/$(ENV) && terraform init -upgrade

docs: ## Generate module documentation (requires terraform-docs)
	@echo "Generating module documentation..."
	@if command -v terraform-docs &> /dev/null; then \
		for dir in modules/*/; do \
			echo "Generating docs for $$dir"; \
			terraform-docs markdown table $$dir > $$dir/README.md; \
		done; \
		echo "Documentation generated!"; \
	else \
		echo "Error: terraform-docs not installed. Install from https://terraform-docs.io/"; \
		exit 1; \
	fi

security-scan: ## Run security scan (requires tfsec)
	@echo "Running security scan..."
	@if command -v tfsec &> /dev/null; then \
		tfsec .; \
	else \
		echo "Error: tfsec not installed. Install from https://github.com/aquasecurity/tfsec"; \
		exit 1; \
	fi

lint: ## Lint Terraform files (requires tflint)
	@echo "Linting Terraform files..."
	@if command -v tflint &> /dev/null; then \
		tflint --recursive; \
	else \
		echo "Error: tflint not installed. Install from https://github.com/terraform-linters/tflint"; \
		exit 1; \
	fi

check-drift: init ## Check for configuration drift
	@echo "Checking for drift in $(ENV)..."
	@cd environments/$(ENV) && terraform plan -detailed-exitcode

# Development shortcuts
dev-init: ## Initialize dev environment
	@make init ENV=dev

dev-plan: ## Plan dev environment
	@make plan ENV=dev

dev-apply: ## Apply dev environment
	@make apply ENV=dev

dev-destroy: ## Destroy dev environment
	@make destroy ENV=dev

# Staging shortcuts
staging-init: ## Initialize staging environment
	@make init ENV=staging

staging-plan: ## Plan staging environment
	@make plan ENV=staging

staging-apply: ## Apply staging environment
	@make apply ENV=staging

staging-destroy: ## Destroy staging environment
	@make destroy ENV=staging

# Production shortcuts
prod-init: ## Initialize production environment
	@make init ENV=production

prod-plan: ## Plan production environment
	@make plan ENV=production

prod-apply: ## Apply production environment (requires confirmation)
	@make apply ENV=production

# Quick start
quickstart: ## Quick start guide
	@echo "===================================="
	@echo "Vidra Core Terraform Quick Start"
	@echo "===================================="
	@echo ""
	@echo "1. Bootstrap backend:"
	@echo "   make bootstrap ENV=production REGION=us-east-1"
	@echo ""
	@echo "2. Configure variables:"
	@echo "   cp environments/production/terraform.tfvars.example environments/production/terraform.tfvars"
	@echo "   # Edit terraform.tfvars with your values"
	@echo ""
	@echo "3. Plan infrastructure:"
	@echo "   make plan ENV=production"
	@echo ""
	@echo "4. Apply infrastructure:"
	@echo "   make apply ENV=production"
	@echo ""
	@echo "5. Deploy Kubernetes manifests:"
	@echo "   make deploy-k8s ENV=production"
	@echo ""
	@echo "6. Check status:"
	@echo "   make outputs ENV=production"
	@echo "   make kubectl-config ENV=production"
	@echo "   kubectl get pods -n vidra-production"
	@echo ""
