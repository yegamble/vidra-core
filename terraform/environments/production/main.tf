# Production Environment Configuration for Vidra Core Platform
# This file orchestrates all infrastructure modules

terraform {
  required_version = ">= 1.6.0"

  backend "s3" {
    # Backend configuration should be provided via backend-config file
    # Example: terraform init -backend-config=backend.hcl
    # Contents of backend.hcl:
    #   bucket         = "vidra-terraform-state-production"
    #   key            = "production/terraform.tfstate"
    #   region         = "us-east-1"
    #   encrypt        = true
    #   dynamodb_table = "vidra-terraform-locks"
  }
}

# Local variables
locals {
  environment = "production"
  region      = var.aws_region

  common_tags = {
    Project     = var.project_name
    Environment = local.environment
    ManagedBy   = "Terraform"
    Owner       = var.owner_email
    CostCenter  = var.cost_center
  }
}

# VPC and Networking
module "networking" {
  source = "../../modules/networking"

  project_name       = var.project_name
  environment        = local.environment
  vpc_cidr           = var.vpc_cidr
  availability_zones = var.availability_zones

  enable_nat_gateway = true
  single_nat_gateway = false # Multi-AZ for production

  enable_flow_logs          = true
  flow_logs_retention_days  = 30

  tags = local.common_tags
}

# EKS Cluster
module "eks" {
  source = "../../modules/eks"

  project_name       = var.project_name
  environment        = local.environment
  cluster_version    = var.eks_cluster_version
  vpc_id             = module.networking.vpc_id
  private_subnet_ids = module.networking.private_subnet_ids
  public_subnet_ids  = module.networking.public_subnet_ids

  cluster_security_group_id = module.networking.eks_cluster_security_group_id

  enable_public_access   = true
  public_access_cidrs    = var.allowed_cidr_blocks
  enable_secrets_encryption = true

  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
  log_retention_days        = 30

  node_groups = var.eks_node_groups

  enable_ebs_csi_driver = true
  enable_efs_csi_driver = true

  tags = local.common_tags

  depends_on = [module.networking]
}

# RDS PostgreSQL
module "rds" {
  source = "../../modules/rds"

  project_name      = var.project_name
  environment       = local.environment
  subnet_ids        = module.networking.database_subnet_ids
  security_group_id = module.networking.rds_security_group_id

  engine_version    = var.rds_engine_version
  instance_class    = var.rds_instance_class
  allocated_storage = var.rds_allocated_storage
  max_allocated_storage = var.rds_max_allocated_storage
  storage_type      = "gp3"

  database_name   = "vidra"
  master_username = "vidraadmin"
  # Password will be auto-generated and stored in Secrets Manager

  multi_az                = true
  backup_retention_period = 30 # 30 days for production
  deletion_protection     = true
  skip_final_snapshot     = false

  enable_encryption            = true
  enable_performance_insights  = true
  monitoring_interval          = 60

  # Performance tuning for xlarge instances
  max_connections         = "500"
  shared_buffers          = "4194304"  # 4GB
  effective_cache_size    = "12582912" # 12GB
  maintenance_work_mem    = "1048576"  # 1GB

  create_cloudwatch_alarms = true
  alarm_actions            = [] # Add SNS topic ARN for alerts

  tags = local.common_tags

  depends_on = [module.networking]
}

# ElastiCache Redis
module "elasticache" {
  source = "../../modules/elasticache"

  project_name      = var.project_name
  environment       = local.environment
  subnet_ids        = module.networking.database_subnet_ids
  security_group_id = module.networking.elasticache_security_group_id

  engine_version     = var.elasticache_engine_version
  node_type          = var.elasticache_node_type
  num_cache_clusters = 2 # Primary + 1 replica

  multi_az_enabled             = true
  enable_encryption_at_rest    = true
  enable_encryption_in_transit = true

  snapshot_retention_limit = 7
  auto_minor_version_upgrade = true

  create_cloudwatch_alarms = true
  alarm_actions            = [] # Add SNS topic ARN for alerts

  tags = local.common_tags

  depends_on = [module.networking]
}

# EFS for shared storage
module "efs" {
  source = "../../modules/efs"

  project_name      = var.project_name
  environment       = local.environment
  subnet_ids        = module.networking.private_subnet_ids
  security_group_id = module.networking.efs_security_group_id

  enable_encryption  = true
  performance_mode   = "generalPurpose"
  throughput_mode    = "bursting"

  transition_to_ia = "AFTER_30_DAYS"
  enable_backup    = true

  create_cloudwatch_alarms = true
  alarm_actions            = [] # Add SNS topic ARN for alerts

  tags = local.common_tags

  depends_on = [module.networking]
}

# S3 and CloudFront for video delivery
module "s3" {
  source = "../../modules/s3"

  project_name = var.project_name
  environment  = local.environment

  enable_kms_encryption = true
  enable_versioning     = true

  lifecycle_rules = var.s3_lifecycle_rules

  enable_cloudfront      = true
  cloudfront_price_class = var.cloudfront_price_class
  cloudfront_min_ttl     = var.cloudfront_min_ttl
  cloudfront_default_ttl = var.cloudfront_default_ttl
  cloudfront_max_ttl     = var.cloudfront_max_ttl

  # Configure with your domain
  cloudfront_aliases = var.cloudfront_aliases
  acm_certificate_arn = var.acm_certificate_arn

  tags = local.common_tags
}

# IAM Role for Service Account (IRSA) - S3 Access
resource "aws_iam_role" "vidra_s3_access" {
  name = "${var.project_name}-${local.environment}-s3-access"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = module.eks.oidc_provider_arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${replace(module.eks.cluster_oidc_issuer_url, "https://", "")}:sub" = "system:serviceaccount:default:vidra-api"
          "${replace(module.eks.cluster_oidc_issuer_url, "https://", "")}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "vidra_s3_access" {
  name = "s3-access"
  role = aws_iam_role.vidra_s3_access.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:PutObject",
          "s3:GetObject",
          "s3:DeleteObject",
          "s3:ListBucket"
        ]
        Resource = [
          module.s3.bucket_arn,
          "${module.s3.bucket_arn}/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:GenerateDataKey"
        ]
        Resource = [module.s3.kms_key_arn]
      }
    ]
  })
}

# IAM Role for Service Account - Secrets Manager Access
resource "aws_iam_role" "vidra_secrets_access" {
  name = "${var.project_name}-${local.environment}-secrets-access"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = module.eks.oidc_provider_arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${replace(module.eks.cluster_oidc_issuer_url, "https://", "")}:sub" = "system:serviceaccount:default:vidra-api"
          "${replace(module.eks.cluster_oidc_issuer_url, "https://", "")}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "vidra_secrets_access" {
  name = "secrets-access"
  role = aws_iam_role.vidra_secrets_access.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = [
          module.rds.secret_arn,
          module.elasticache.auth_token_secret_arn != null ? module.elasticache.auth_token_secret_arn : "*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt"
        ]
        Resource = [
          module.rds.kms_key_arn
        ]
      }
    ]
  })
}
