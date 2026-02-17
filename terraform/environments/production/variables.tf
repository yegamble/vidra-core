# Production Environment Variables
# These variables are set via terraform.tfvars

variable "project_name" {
  description = "Project name"
  type        = string
}

variable "environment" {
  description = "Environment name"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}

variable "owner_email" {
  description = "Owner email"
  type        = string
}

variable "cost_center" {
  description = "Cost center"
  type        = string
}

variable "domain_name" {
  description = "Domain name"
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR block"
  type        = string
}

variable "availability_zones" {
  description = "Availability zones"
  type        = list(string)
}

variable "enable_multi_az" {
  description = "Enable Multi-AZ"
  type        = bool
}

variable "enable_deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
}

variable "enable_backup" {
  description = "Enable backups"
  type        = bool
}

variable "backup_retention_days" {
  description = "Backup retention days"
  type        = number
}

variable "eks_cluster_version" {
  description = "EKS cluster version"
  type        = string
}

variable "eks_node_groups" {
  description = "EKS node groups configuration"
  type = map(object({
    instance_types = list(string)
    capacity_type  = string
    min_size       = number
    max_size       = number
    desired_size   = number
    disk_size      = number
    labels         = map(string)
    taints = list(object({
      key    = string
      value  = string
      effect = string
    }))
  }))
}

variable "rds_instance_class" {
  description = "RDS instance class"
  type        = string
}

variable "rds_allocated_storage" {
  description = "RDS allocated storage"
  type        = number
}

variable "rds_max_allocated_storage" {
  description = "RDS max allocated storage"
  type        = number
}

variable "rds_engine_version" {
  description = "RDS engine version"
  type        = string
}

variable "elasticache_node_type" {
  description = "ElastiCache node type"
  type        = string
}

variable "elasticache_num_cache_nodes" {
  description = "ElastiCache number of nodes"
  type        = number
}

variable "elasticache_engine_version" {
  description = "ElastiCache engine version"
  type        = string
}

variable "efs_performance_mode" {
  description = "EFS performance mode"
  type        = string
}

variable "efs_throughput_mode" {
  description = "EFS throughput mode"
  type        = string
}

variable "s3_versioning_enabled" {
  description = "Enable S3 versioning"
  type        = bool
}

variable "s3_lifecycle_rules" {
  description = "S3 lifecycle rules"
  type = list(object({
    id      = string
    enabled = bool
    transitions = list(object({
      days          = number
      storage_class = string
    }))
    expiration_days = number
  }))
}

variable "cloudfront_price_class" {
  description = "CloudFront price class"
  type        = string
}

variable "cloudfront_min_ttl" {
  description = "CloudFront min TTL"
  type        = number
}

variable "cloudfront_default_ttl" {
  description = "CloudFront default TTL"
  type        = number
}

variable "cloudfront_max_ttl" {
  description = "CloudFront max TTL"
  type        = number
}

variable "cloudfront_aliases" {
  description = "CloudFront aliases"
  type        = list(string)
}

variable "acm_certificate_arn" {
  description = "ACM certificate ARN"
  type        = string
}

variable "enable_prometheus_operator" {
  description = "Enable Prometheus operator"
  type        = bool
}

variable "enable_grafana" {
  description = "Enable Grafana"
  type        = bool
}

variable "cloudwatch_log_retention_days" {
  description = "CloudWatch log retention days"
  type        = number
}

variable "allowed_cidr_blocks" {
  description = "List of allowed CIDR blocks for external access. DO NOT use 0.0.0.0/0 in production for security reasons."
  type        = list(string)
}

variable "enable_waf" {
  description = "Enable WAF"
  type        = bool
}

variable "enable_kms_encryption" {
  description = "Enable KMS encryption"
  type        = bool
}

variable "additional_tags" {
  description = "Additional tags"
  type        = map(string)
}
