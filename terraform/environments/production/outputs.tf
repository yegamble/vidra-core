# Production Environment Outputs

# VPC Outputs
output "vpc_id" {
  description = "VPC ID"
  value       = module.networking.vpc_id
}

output "private_subnet_ids" {
  description = "Private subnet IDs"
  value       = module.networking.private_subnet_ids
}

# EKS Outputs
output "eks_cluster_name" {
  description = "EKS cluster name"
  value       = module.eks.cluster_name
}

output "eks_cluster_endpoint" {
  description = "EKS cluster endpoint"
  value       = module.eks.cluster_endpoint
}

output "eks_cluster_version" {
  description = "EKS cluster Kubernetes version"
  value       = module.eks.cluster_version
}

output "configure_kubectl" {
  description = "Command to configure kubectl"
  value       = "aws eks update-kubeconfig --region ${var.aws_region} --name ${module.eks.cluster_name}"
}

# RDS Outputs
output "rds_endpoint" {
  description = "RDS instance endpoint"
  value       = module.rds.endpoint
  sensitive   = true
}

output "rds_secret_name" {
  description = "Name of Secrets Manager secret containing DB credentials"
  value       = module.rds.secret_name
}

output "database_connection_string" {
  description = "Database connection string (without password)"
  value       = module.rds.connection_string
  sensitive   = true
}

# ElastiCache Outputs
output "redis_endpoint" {
  description = "Redis configuration endpoint"
  value       = module.elasticache.configuration_endpoint_address
  sensitive   = true
}

output "redis_connection_string" {
  description = "Redis connection string"
  value       = module.elasticache.connection_string
  sensitive   = true
}

output "redis_secret_name" {
  description = "Name of Secrets Manager secret containing Redis auth token"
  value       = module.elasticache.auth_token_secret_name
}

# EFS Outputs
output "efs_id" {
  description = "EFS file system ID"
  value       = module.efs.file_system_id
}

output "efs_dns_name" {
  description = "EFS DNS name"
  value       = module.efs.file_system_dns_name
}

output "efs_storage_access_point_id" {
  description = "EFS access point ID for storage"
  value       = module.efs.storage_access_point_id
}

output "efs_quarantine_access_point_id" {
  description = "EFS access point ID for quarantine"
  value       = module.efs.quarantine_access_point_id
}

# S3 Outputs
output "s3_bucket_name" {
  description = "S3 bucket name for videos"
  value       = module.s3.bucket_name
}

output "s3_bucket_arn" {
  description = "S3 bucket ARN"
  value       = module.s3.bucket_arn
}

output "cloudfront_domain_name" {
  description = "CloudFront distribution domain name"
  value       = module.s3.cloudfront_domain_name
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID"
  value       = module.s3.cloudfront_distribution_id
}

# IAM Outputs
output "s3_access_role_arn" {
  description = "IAM role ARN for S3 access (for Kubernetes ServiceAccount)"
  value       = aws_iam_role.athena_s3_access.arn
}

output "secrets_access_role_arn" {
  description = "IAM role ARN for Secrets Manager access (for Kubernetes ServiceAccount)"
  value       = aws_iam_role.athena_secrets_access.arn
}

# Connection Information
output "connection_summary" {
  description = "Summary of connection information"
  value = {
    eks_cluster    = module.eks.cluster_name
    rds_endpoint   = module.rds.address
    redis_endpoint = module.elasticache.configuration_endpoint_address
    efs_dns_name   = module.efs.file_system_dns_name
    s3_bucket      = module.s3.bucket_name
    cdn_domain     = module.s3.cloudfront_domain_name
  }
}
