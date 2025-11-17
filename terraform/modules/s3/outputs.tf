output "bucket_id" {
  description = "S3 bucket ID"
  value       = aws_s3_bucket.videos.id
}

output "bucket_arn" {
  description = "S3 bucket ARN"
  value       = aws_s3_bucket.videos.arn
}

output "bucket_name" {
  description = "S3 bucket name"
  value       = aws_s3_bucket.videos.bucket
}

output "bucket_regional_domain_name" {
  description = "S3 bucket regional domain name"
  value       = aws_s3_bucket.videos.bucket_regional_domain_name
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID"
  value       = var.enable_cloudfront ? aws_cloudfront_distribution.videos[0].id : null
}

output "cloudfront_distribution_arn" {
  description = "CloudFront distribution ARN"
  value       = var.enable_cloudfront ? aws_cloudfront_distribution.videos[0].arn : null
}

output "cloudfront_domain_name" {
  description = "CloudFront distribution domain name"
  value       = var.enable_cloudfront ? aws_cloudfront_distribution.videos[0].domain_name : null
}

output "cloudfront_hosted_zone_id" {
  description = "CloudFront hosted zone ID for Route53 alias"
  value       = var.enable_cloudfront ? aws_cloudfront_distribution.videos[0].hosted_zone_id : null
}

output "kms_key_id" {
  description = "KMS key ID for S3 encryption"
  value       = var.enable_kms_encryption ? aws_kms_key.s3[0].key_id : null
}

output "kms_key_arn" {
  description = "KMS key ARN for S3 encryption"
  value       = var.enable_kms_encryption ? aws_kms_key.s3[0].arn : null
}
