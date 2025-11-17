output "replication_group_id" {
  description = "ElastiCache replication group ID"
  value       = aws_elasticache_replication_group.main.id
}

output "replication_group_arn" {
  description = "ElastiCache replication group ARN"
  value       = aws_elasticache_replication_group.main.arn
}

output "primary_endpoint_address" {
  description = "Primary endpoint address"
  value       = aws_elasticache_replication_group.main.primary_endpoint_address
}

output "reader_endpoint_address" {
  description = "Reader endpoint address"
  value       = aws_elasticache_replication_group.main.reader_endpoint_address
}

output "configuration_endpoint_address" {
  description = "Configuration endpoint address"
  value       = aws_elasticache_replication_group.main.configuration_endpoint_address
}

output "port" {
  description = "Redis port"
  value       = var.port
}

output "connection_string" {
  description = "Redis connection string (without auth token)"
  value       = var.enable_encryption_in_transit ? "rediss://${aws_elasticache_replication_group.main.configuration_endpoint_address}:${var.port}" : "redis://${aws_elasticache_replication_group.main.configuration_endpoint_address}:${var.port}"
  sensitive   = true
}

output "auth_token_secret_arn" {
  description = "ARN of the Secrets Manager secret containing auth token"
  value       = var.enable_encryption_in_transit ? aws_secretsmanager_secret.auth_token[0].arn : null
}

output "auth_token_secret_name" {
  description = "Name of the Secrets Manager secret containing auth token"
  value       = var.enable_encryption_in_transit ? aws_secretsmanager_secret.auth_token[0].name : null
}

output "parameter_group_id" {
  description = "ElastiCache parameter group ID"
  value       = aws_elasticache_parameter_group.main.id
}

output "subnet_group_name" {
  description = "ElastiCache subnet group name"
  value       = aws_elasticache_subnet_group.main.name
}
