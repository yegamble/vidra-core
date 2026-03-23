# S3 and CloudFront Module for Vidra Core Platform
# Creates S3 buckets for video storage with CloudFront CDN for delivery

locals {
  name = "${var.project_name}-${var.environment}"

  common_tags = merge(
    var.tags,
    {
      Module = "s3"
    }
  )
}

# KMS Key for S3 encryption
resource "aws_kms_key" "s3" {
  count = var.enable_kms_encryption ? 1 : 0

  description             = "KMS key for S3 bucket encryption"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  tags = local.common_tags
}

resource "aws_kms_alias" "s3" {
  count = var.enable_kms_encryption ? 1 : 0

  name          = "alias/${local.name}-s3"
  target_key_id = aws_kms_key.s3[0].key_id
}

# S3 Bucket for video storage
resource "aws_s3_bucket" "videos" {
  bucket = "${local.name}-videos-${data.aws_caller_identity.current.account_id}"

  tags = merge(
    local.common_tags,
    {
      Name = "${local.name}-videos"
    }
  )
}

# S3 Bucket Versioning
resource "aws_s3_bucket_versioning" "videos" {
  bucket = aws_s3_bucket.videos.id

  versioning_configuration {
    status = var.enable_versioning ? "Enabled" : "Suspended"
  }
}

# S3 Bucket Encryption
resource "aws_s3_bucket_server_side_encryption_configuration" "videos" {
  bucket = aws_s3_bucket.videos.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.enable_kms_encryption ? "aws:kms" : "AES256"
      kms_master_key_id = var.enable_kms_encryption ? aws_kms_key.s3[0].arn : null
    }
    bucket_key_enabled = var.enable_kms_encryption
  }
}

# S3 Bucket Public Access Block
resource "aws_s3_bucket_public_access_block" "videos" {
  bucket = aws_s3_bucket.videos.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# S3 Bucket Lifecycle Configuration
resource "aws_s3_bucket_lifecycle_configuration" "videos" {
  count = length(var.lifecycle_rules) > 0 ? 1 : 0

  bucket = aws_s3_bucket.videos.id

  dynamic "rule" {
    for_each = var.lifecycle_rules
    content {
      id     = rule.value.id
      status = rule.value.enabled ? "Enabled" : "Disabled"

      dynamic "transition" {
        for_each = rule.value.transitions
        content {
          days          = transition.value.days
          storage_class = transition.value.storage_class
        }
      }

      dynamic "expiration" {
        for_each = rule.value.expiration_days != null ? [1] : []
        content {
          days = rule.value.expiration_days
        }
      }

      dynamic "noncurrent_version_transition" {
        for_each = var.enable_versioning ? rule.value.transitions : []
        content {
          noncurrent_days = noncurrent_version_transition.value.days
          storage_class   = noncurrent_version_transition.value.storage_class
        }
      }

      dynamic "noncurrent_version_expiration" {
        for_each = var.enable_versioning && rule.value.expiration_days != null ? [1] : []
        content {
          noncurrent_days = rule.value.expiration_days
        }
      }
    }
  }
}

# S3 Bucket CORS Configuration
resource "aws_s3_bucket_cors_configuration" "videos" {
  count = var.enable_cors ? 1 : 0

  bucket = aws_s3_bucket.videos.id

  cors_rule {
    allowed_headers = var.cors_allowed_headers
    allowed_methods = var.cors_allowed_methods
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag", "x-amz-server-side-encryption", "x-amz-request-id", "x-amz-id-2"]
    max_age_seconds = 3600
  }
}

# CloudFront Origin Access Control
resource "aws_cloudfront_origin_access_control" "videos" {
  count = var.enable_cloudfront ? 1 : 0

  name                              = "${local.name}-videos"
  description                       = "OAC for ${local.name} videos bucket"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

# CloudFront Distribution
resource "aws_cloudfront_distribution" "videos" {
  count = var.enable_cloudfront ? 1 : 0

  enabled             = true
  is_ipv6_enabled     = true
  comment             = "CDN for ${local.name} videos"
  default_root_object = ""
  price_class         = var.cloudfront_price_class
  aliases             = var.cloudfront_aliases

  origin {
    domain_name              = aws_s3_bucket.videos.bucket_regional_domain_name
    origin_id                = "S3-${aws_s3_bucket.videos.id}"
    origin_access_control_id = aws_cloudfront_origin_access_control.videos[0].id
  }

  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.videos.id}"

    forwarded_values {
      query_string = true
      headers      = ["Origin", "Access-Control-Request-Headers", "Access-Control-Request-Method"]

      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = var.cloudfront_min_ttl
    default_ttl            = var.cloudfront_default_ttl
    max_ttl                = var.cloudfront_max_ttl
    compress               = true

    function_association {
      event_type   = "viewer-request"
      function_arn = var.cloudfront_function_arn != null ? var.cloudfront_function_arn : null
    }
  }

  # Cache behavior for video files
  ordered_cache_behavior {
    path_pattern     = "*.mp4"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.videos.id}"

    forwarded_values {
      query_string = false

      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 86400   # 1 day
    max_ttl                = 31536000 # 1 year
    compress               = false    # Don't compress videos
  }

  # Cache behavior for thumbnails
  ordered_cache_behavior {
    path_pattern     = "*.jpg"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.videos.id}"

    forwarded_values {
      query_string = false

      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 86400   # 1 day
    max_ttl                = 31536000 # 1 year
    compress               = true
  }

  restrictions {
    geo_restriction {
      restriction_type = var.geo_restriction_type
      locations        = var.geo_restriction_locations
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = length(var.cloudfront_aliases) == 0
    acm_certificate_arn            = length(var.cloudfront_aliases) > 0 ? var.acm_certificate_arn : null
    ssl_support_method             = length(var.cloudfront_aliases) > 0 ? "sni-only" : null
    minimum_protocol_version       = "TLSv1.2_2021"
  }

  tags = local.common_tags
}

# S3 Bucket Policy to allow CloudFront access
resource "aws_s3_bucket_policy" "videos" {
  count = var.enable_cloudfront ? 1 : 0

  bucket = aws_s3_bucket.videos.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowCloudFrontServicePrincipal"
        Effect = "Allow"
        Principal = {
          Service = "cloudfront.amazonaws.com"
        }
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.videos.arn}/*"
        Condition = {
          StringEquals = {
            "AWS:SourceArn" = aws_cloudfront_distribution.videos[0].arn
          }
        }
      }
    ]
  })
}

# Data source for current AWS account
data "aws_caller_identity" "current" {}
