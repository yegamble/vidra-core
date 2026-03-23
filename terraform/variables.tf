# Global Variables for Vidra Core Infrastructure

variable "aws_region" {
  description = "AWS region for all resources"
  type        = string
  default     = "us-east-1"
}

variable "project_name" {
  description = "Project name used for resource naming"
  type        = string
  default     = "vidra"
}

variable "environment" {
  description = "Environment name (dev, staging, production)"
  type        = string
  validation {
    condition     = contains(["dev", "staging", "production"], var.environment)
    error_message = "Environment must be dev, staging, or production."
  }
}

variable "owner_email" {
  description = "Email of the infrastructure owner"
  type        = string
}

variable "cost_center" {
  description = "Cost center for billing allocation"
  type        = string
  default     = "engineering"
}

variable "domain_name" {
  description = "Domain name for the application (e.g., vidra.example.com)"
  type        = string
}

variable "enable_multi_az" {
  description = "Enable Multi-AZ deployment for high availability"
  type        = bool
  default     = true
}

variable "enable_deletion_protection" {
  description = "Enable deletion protection for critical resources (RDS, etc.)"
  type        = bool
  default     = true
}

variable "enable_backup" {
  description = "Enable automated backups"
  type        = bool
  default     = true
}

variable "backup_retention_days" {
  description = "Number of days to retain backups"
  type        = number
  default     = 7
}

# VPC Configuration
variable "vpc_cidr" {
  description = "CIDR block for VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "List of availability zones to use"
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b", "us-east-1c"]
}

# EKS Configuration
variable "eks_cluster_version" {
  description = "Kubernetes version for EKS cluster"
  type        = string
  default     = "1.28"
}

variable "eks_node_groups" {
  description = "EKS node group configurations"
  type = map(object({
    instance_types = list(string)
    capacity_type  = string # ON_DEMAND or SPOT
    min_size      = number
    max_size      = number
    desired_size  = number
    disk_size     = number
    labels        = map(string)
    taints = list(object({
      key    = string
      value  = string
      effect = string
    }))
  }))
  default = {
    api = {
      instance_types = ["t3.xlarge"]
      capacity_type  = "ON_DEMAND"
      min_size      = 2
      max_size      = 10
      desired_size  = 3
      disk_size     = 100
      labels = {
        workload = "api"
      }
      taints = []
    }
    encoding = {
      instance_types = ["c6i.4xlarge", "c5.4xlarge"]
      capacity_type  = "SPOT"
      min_size      = 1
      max_size      = 10
      desired_size  = 2
      disk_size     = 200
      labels = {
        workload = "encoding"
      }
      taints = [{
        key    = "workload"
        value  = "encoding"
        effect = "NoSchedule"
      }]
    }
  }
}

# RDS Configuration
variable "rds_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.r6g.xlarge"
}

variable "rds_allocated_storage" {
  description = "Initial allocated storage in GB"
  type        = number
  default     = 100
}

variable "rds_max_allocated_storage" {
  description = "Maximum storage for autoscaling in GB"
  type        = number
  default     = 1000
}

variable "rds_engine_version" {
  description = "PostgreSQL engine version"
  type        = string
  default     = "15.4"
}

# ElastiCache Configuration
variable "elasticache_node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.r6g.large"
}

variable "elasticache_num_cache_nodes" {
  description = "Number of cache nodes"
  type        = number
  default     = 2
}

variable "elasticache_engine_version" {
  description = "Redis engine version"
  type        = string
  default     = "7.0"
}

# EFS Configuration
variable "efs_performance_mode" {
  description = "EFS performance mode (generalPurpose or maxIO)"
  type        = string
  default     = "generalPurpose"
}

variable "efs_throughput_mode" {
  description = "EFS throughput mode (bursting or provisioned)"
  type        = string
  default     = "bursting"
}

variable "efs_provisioned_throughput_in_mibps" {
  description = "Provisioned throughput in MiB/s (only for provisioned mode)"
  type        = number
  default     = null
}

# S3 Configuration
variable "s3_versioning_enabled" {
  description = "Enable S3 versioning"
  type        = bool
  default     = true
}

variable "s3_lifecycle_rules" {
  description = "S3 lifecycle rules for cost optimization"
  type = list(object({
    id      = string
    enabled = bool
    transitions = list(object({
      days          = number
      storage_class = string
    }))
    expiration_days = number
  }))
  default = [
    {
      id      = "archive-old-videos"
      enabled = true
      transitions = [
        {
          days          = 30
          storage_class = "STANDARD_IA"
        },
        {
          days          = 90
          storage_class = "GLACIER"
        }
      ]
      expiration_days = 365
    }
  ]
}

# CloudFront Configuration
variable "cloudfront_price_class" {
  description = "CloudFront price class (PriceClass_All, PriceClass_200, PriceClass_100)"
  type        = string
  default     = "PriceClass_100" # US, Canada, Europe
}

variable "cloudfront_min_ttl" {
  description = "Minimum TTL for CloudFront caching"
  type        = number
  default     = 0
}

variable "cloudfront_default_ttl" {
  description = "Default TTL for CloudFront caching"
  type        = number
  default     = 86400 # 24 hours
}

variable "cloudfront_max_ttl" {
  description = "Maximum TTL for CloudFront caching"
  type        = number
  default     = 31536000 # 1 year
}

# Monitoring Configuration
variable "enable_prometheus_operator" {
  description = "Install Prometheus Operator in EKS cluster"
  type        = bool
  default     = true
}

variable "enable_grafana" {
  description = "Install Grafana in EKS cluster"
  type        = bool
  default     = true
}

variable "cloudwatch_log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 7
}

# Security Configuration
variable "allowed_cidr_blocks" {
  description = "CIDR blocks allowed to access the application"
  type        = list(string)
}

variable "enable_waf" {
  description = "Enable AWS WAF for application protection"
  type        = bool
  default     = true
}

variable "enable_kms_encryption" {
  description = "Enable KMS encryption for EBS, RDS, S3"
  type        = bool
  default     = true
}

# Tags
variable "additional_tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
