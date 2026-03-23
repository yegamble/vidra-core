# Remote State Backend Configuration
# This file configures S3 backend for Terraform state storage with DynamoDB locking
#
# State is encrypted at rest and supports team collaboration with state locking
# Different environments use different state files via workspace or key prefix

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.23"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.11"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.5"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # Backend configuration is environment-specific
  # Uncomment and configure after bootstrapping the backend
  # See scripts/bootstrap-backend.sh

  # backend "s3" {
  #   # Backend configuration is provided via backend-config file or -backend-config flags
  #   # Example: terraform init -backend-config=backend-dev.hcl
  # }
}
