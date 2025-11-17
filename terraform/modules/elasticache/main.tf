# ElastiCache Redis Module for Athena Platform
# Creates Redis cluster with automatic failover and encryption

locals {
  name = "${var.project_name}-${var.environment}-redis"

  common_tags = merge(
    var.tags,
    {
      Name   = local.name
      Module = "elasticache"
    }
  )
}

# Subnet Group
resource "aws_elasticache_subnet_group" "main" {
  name       = local.name
  subnet_ids = var.subnet_ids

  tags = local.common_tags
}

# Parameter Group
resource "aws_elasticache_parameter_group" "main" {
  name   = local.name
  family = var.parameter_group_family

  # Redis configuration parameters
  parameter {
    name  = "maxmemory-policy"
    value = var.maxmemory_policy
  }

  parameter {
    name  = "timeout"
    value = "300"
  }

  parameter {
    name  = "tcp-keepalive"
    value = "300"
  }

  parameter {
    name  = "notify-keyspace-events"
    value = "Ex" # Enable keyspace notifications for expired events
  }

  tags = local.common_tags

  lifecycle {
    create_before_destroy = true
  }
}

# Replication Group (Redis Cluster)
resource "aws_elasticache_replication_group" "main" {
  replication_group_id       = local.name
  replication_group_description = "Redis cluster for ${var.project_name} ${var.environment}"

  engine               = "redis"
  engine_version       = var.engine_version
  node_type            = var.node_type
  port                 = var.port
  parameter_group_name = aws_elasticache_parameter_group.main.name

  # Cluster configuration
  num_cache_clusters         = var.num_cache_clusters
  automatic_failover_enabled = var.num_cache_clusters > 1 ? true : false
  multi_az_enabled          = var.multi_az_enabled && var.num_cache_clusters > 1

  # Network
  subnet_group_name  = aws_elasticache_subnet_group.main.name
  security_group_ids = [var.security_group_id]

  # Encryption
  at_rest_encryption_enabled = var.enable_encryption_at_rest
  transit_encryption_enabled = var.enable_encryption_in_transit
  auth_token_enabled        = var.enable_encryption_in_transit
  auth_token                = var.enable_encryption_in_transit ? (var.auth_token != null ? var.auth_token : random_password.auth_token[0].result) : null

  # Maintenance
  maintenance_window         = var.maintenance_window
  snapshot_window           = var.snapshot_window
  snapshot_retention_limit  = var.snapshot_retention_limit
  auto_minor_version_upgrade = var.auto_minor_version_upgrade
  apply_immediately         = var.apply_immediately

  # Notifications
  notification_topic_arn = var.notification_topic_arn

  # Logging
  log_delivery_configuration {
    destination      = aws_cloudwatch_log_group.slow_log.name
    destination_type = "cloudwatch-logs"
    log_format       = "json"
    log_type         = "slow-log"
  }

  log_delivery_configuration {
    destination      = aws_cloudwatch_log_group.engine_log.name
    destination_type = "cloudwatch-logs"
    log_format       = "json"
    log_type         = "engine-log"
  }

  tags = local.common_tags

  lifecycle {
    ignore_changes = [auth_token]
  }
}

# Generate random auth token if encryption in transit is enabled
resource "random_password" "auth_token" {
  count = var.enable_encryption_in_transit && var.auth_token == null ? 1 : 0

  length  = 32
  special = true
  override_special = "!&#$^<>-"
}

# Store auth token in Secrets Manager
resource "aws_secretsmanager_secret" "auth_token" {
  count = var.enable_encryption_in_transit ? 1 : 0

  name                    = "${local.name}-auth-token"
  description             = "Redis auth token for ${local.name}"
  recovery_window_in_days = 0

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "auth_token" {
  count = var.enable_encryption_in_transit ? 1 : 0

  secret_id = aws_secretsmanager_secret.auth_token[0].id
  secret_string = jsonencode({
    auth_token = var.auth_token != null ? var.auth_token : random_password.auth_token[0].result
    endpoint   = aws_elasticache_replication_group.main.configuration_endpoint_address
    port       = var.port
  })
}

# CloudWatch Log Groups
resource "aws_cloudwatch_log_group" "slow_log" {
  name              = "/aws/elasticache/${local.name}/slow-log"
  retention_in_days = var.log_retention_days

  tags = local.common_tags
}

resource "aws_cloudwatch_log_group" "engine_log" {
  name              = "/aws/elasticache/${local.name}/engine-log"
  retention_in_days = var.log_retention_days

  tags = local.common_tags
}

# CloudWatch Alarms
resource "aws_cloudwatch_metric_alarm" "cache_cpu" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ElastiCache"
  period              = "300"
  statistic           = "Average"
  threshold           = "75"
  alarm_description   = "This metric monitors ElastiCache CPU utilization"
  alarm_actions       = var.alarm_actions

  dimensions = {
    ReplicationGroupId = aws_elasticache_replication_group.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "cache_memory" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-high-memory"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "DatabaseMemoryUsagePercentage"
  namespace           = "AWS/ElastiCache"
  period              = "300"
  statistic           = "Average"
  threshold           = "90"
  alarm_description   = "This metric monitors ElastiCache memory usage"
  alarm_actions       = var.alarm_actions

  dimensions = {
    ReplicationGroupId = aws_elasticache_replication_group.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "evictions" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-evictions"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "1"
  metric_name         = "Evictions"
  namespace           = "AWS/ElastiCache"
  period              = "300"
  statistic           = "Sum"
  threshold           = "1000"
  alarm_description   = "This metric monitors ElastiCache evictions"
  alarm_actions       = var.alarm_actions

  dimensions = {
    ReplicationGroupId = aws_elasticache_replication_group.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "connections" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-high-connections"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "CurrConnections"
  namespace           = "AWS/ElastiCache"
  period              = "300"
  statistic           = "Average"
  threshold           = "65000" # Default max connections
  alarm_description   = "This metric monitors ElastiCache connections"
  alarm_actions       = var.alarm_actions

  dimensions = {
    ReplicationGroupId = aws_elasticache_replication_group.main.id
  }

  tags = local.common_tags
}
