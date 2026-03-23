output "file_system_id" {
  description = "EFS file system ID"
  value       = aws_efs_file_system.main.id
}

output "file_system_arn" {
  description = "EFS file system ARN"
  value       = aws_efs_file_system.main.arn
}

output "file_system_dns_name" {
  description = "EFS file system DNS name"
  value       = aws_efs_file_system.main.dns_name
}

output "mount_target_ids" {
  description = "List of EFS mount target IDs"
  value       = aws_efs_mount_target.main[*].id
}

output "mount_target_dns_names" {
  description = "List of EFS mount target DNS names"
  value       = aws_efs_mount_target.main[*].dns_name
}

output "storage_access_point_id" {
  description = "EFS access point ID for storage"
  value       = aws_efs_access_point.storage.id
}

output "storage_access_point_arn" {
  description = "EFS access point ARN for storage"
  value       = aws_efs_access_point.storage.arn
}

output "quarantine_access_point_id" {
  description = "EFS access point ID for quarantine"
  value       = aws_efs_access_point.quarantine.id
}

output "quarantine_access_point_arn" {
  description = "EFS access point ARN for quarantine"
  value       = aws_efs_access_point.quarantine.arn
}

output "kms_key_id" {
  description = "KMS key ID for EFS encryption"
  value       = var.enable_encryption ? aws_kms_key.efs[0].key_id : null
}

output "kms_key_arn" {
  description = "KMS key ARN for EFS encryption"
  value       = var.enable_encryption ? aws_kms_key.efs[0].arn : null
}
