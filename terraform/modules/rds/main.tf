# RDS PostgreSQL Module for Vidra Core Platform
# Creates Multi-AZ PostgreSQL database with automated backups and encryption

locals {
  name = "${var.project_name}-${var.environment}-postgres"

  common_tags = merge(
    var.tags,
    {
      Name   = local.name
      Module = "rds"
    }
  )
}

# KMS Key for RDS encryption
resource "aws_kms_key" "rds" {
  count = var.enable_encryption ? 1 : 0

  description             = "KMS key for RDS encryption"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  tags = local.common_tags
}

resource "aws_kms_alias" "rds" {
  count = var.enable_encryption ? 1 : 0

  name          = "alias/${local.name}"
  target_key_id = aws_kms_key.rds[0].key_id
}

# DB Subnet Group
resource "aws_db_subnet_group" "main" {
  name       = local.name
  subnet_ids = var.subnet_ids

  tags = local.common_tags
}

# DB Parameter Group
resource "aws_db_parameter_group" "main" {
  name   = local.name
  family = var.parameter_group_family

  # PostgreSQL performance tuning parameters
  parameter {
    name  = "shared_buffers"
    value = var.shared_buffers
  }

  parameter {
    name  = "max_connections"
    value = var.max_connections
  }

  parameter {
    name  = "effective_cache_size"
    value = var.effective_cache_size
  }

  parameter {
    name  = "maintenance_work_mem"
    value = var.maintenance_work_mem
  }

  parameter {
    name  = "checkpoint_completion_target"
    value = "0.9"
  }

  parameter {
    name  = "wal_buffers"
    value = "16384" # 16MB
  }

  parameter {
    name  = "default_statistics_target"
    value = "100"
  }

  parameter {
    name  = "random_page_cost"
    value = "1.1" # For SSD storage
  }

  parameter {
    name  = "effective_io_concurrency"
    value = "200" # For SSD storage
  }

  parameter {
    name  = "work_mem"
    value = "4096" # 4MB
  }

  parameter {
    name  = "min_wal_size"
    value = "1024" # 1GB
  }

  parameter {
    name  = "max_wal_size"
    value = "4096" # 4GB
  }

  # Logging
  parameter {
    name  = "log_min_duration_statement"
    value = var.log_min_duration_statement
  }

  parameter {
    name  = "log_connections"
    value = "1"
  }

  parameter {
    name  = "log_disconnections"
    value = "1"
  }

  parameter {
    name  = "log_lock_waits"
    value = "1"
  }

  parameter {
    name  = "log_statement"
    value = "ddl"
  }

  parameter {
    name  = "log_temp_files"
    value = "0"
  }

  # Extensions
  parameter {
    name  = "shared_preload_libraries"
    value = "pg_stat_statements"
  }

  tags = local.common_tags

  lifecycle {
    create_before_destroy = true
  }
}

# Generate random password if not provided
resource "random_password" "master" {
  count = var.master_password == null ? 1 : 0

  length  = 32
  special = true
  # Avoid characters that might cause issues in connection strings
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

# Store password in Secrets Manager
resource "aws_secretsmanager_secret" "db_password" {
  name                    = "${local.name}-master-password"
  description             = "Master password for ${local.name}"
  recovery_window_in_days = var.deletion_protection ? 30 : 0

  tags = local.common_tags
}

resource "aws_secretsmanager_secret_version" "db_password" {
  secret_id = aws_secretsmanager_secret.db_password.id
  secret_string = jsonencode({
    username = var.master_username
    password = var.master_password != null ? var.master_password : random_password.master[0].result
    engine   = "postgres"
    host     = aws_db_instance.main.address
    port     = aws_db_instance.main.port
    dbname   = var.database_name
  })
}

# RDS Instance
resource "aws_db_instance" "main" {
  identifier     = local.name
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = var.instance_class

  # Storage
  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage
  storage_type          = var.storage_type
  storage_encrypted     = var.enable_encryption
  kms_key_id            = var.enable_encryption ? aws_kms_key.rds[0].arn : null
  iops                  = var.storage_type == "io1" || var.storage_type == "io2" ? var.iops : null

  # Database
  db_name  = var.database_name
  username = var.master_username
  password = var.master_password != null ? var.master_password : random_password.master[0].result
  port     = var.port

  # Network
  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [var.security_group_id]
  publicly_accessible    = false

  # High Availability
  multi_az = var.multi_az

  # Backup
  backup_retention_period   = var.backup_retention_period
  backup_window             = var.backup_window
  copy_tags_to_snapshot     = true
  delete_automated_backups  = var.environment != "production"
  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${local.name}-final-snapshot-${formatdate("YYYY-MM-DD-hhmm", timestamp())}"

  # Maintenance
  maintenance_window              = var.maintenance_window
  auto_minor_version_upgrade      = var.auto_minor_version_upgrade
  allow_major_version_upgrade     = false
  apply_immediately               = var.apply_immediately
  deletion_protection             = var.deletion_protection
  enabled_cloudwatch_logs_exports = ["postgresql", "upgrade"]

  # Performance Insights
  performance_insights_enabled    = var.enable_performance_insights
  performance_insights_kms_key_id = var.enable_encryption ? aws_kms_key.rds[0].arn : null
  performance_insights_retention_period = var.enable_performance_insights ? var.performance_insights_retention_period : null

  # Monitoring
  monitoring_interval = var.monitoring_interval
  monitoring_role_arn = var.monitoring_interval > 0 ? aws_iam_role.rds_monitoring[0].arn : null

  # Parameters
  parameter_group_name = aws_db_parameter_group.main.name

  tags = local.common_tags

  lifecycle {
    ignore_changes = [
      password,
      final_snapshot_identifier,
    ]
  }
}

# IAM Role for Enhanced Monitoring
resource "aws_iam_role" "rds_monitoring" {
  count = var.monitoring_interval > 0 ? 1 : 0

  name = "${local.name}-monitoring-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "monitoring.rds.amazonaws.com"
        }
      }
    ]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  count = var.monitoring_interval > 0 ? 1 : 0

  role       = aws_iam_role.rds_monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

# CloudWatch Alarms
resource "aws_cloudwatch_metric_alarm" "database_cpu" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "CPUUtilization"
  namespace           = "AWS/RDS"
  period              = "300"
  statistic           = "Average"
  threshold           = "80"
  alarm_description   = "This metric monitors RDS CPU utilization"
  alarm_actions       = var.alarm_actions

  dimensions = {
    DBInstanceIdentifier = aws_db_instance.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "database_memory" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-low-memory"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "FreeableMemory"
  namespace           = "AWS/RDS"
  period              = "300"
  statistic           = "Average"
  threshold           = "536870912" # 512 MB
  alarm_description   = "This metric monitors RDS freeable memory"
  alarm_actions       = var.alarm_actions

  dimensions = {
    DBInstanceIdentifier = aws_db_instance.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "database_storage" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-low-storage"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = "1"
  metric_name         = "FreeStorageSpace"
  namespace           = "AWS/RDS"
  period              = "300"
  statistic           = "Average"
  threshold           = "10737418240" # 10 GB
  alarm_description   = "This metric monitors RDS free storage space"
  alarm_actions       = var.alarm_actions

  dimensions = {
    DBInstanceIdentifier = aws_db_instance.main.id
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_metric_alarm" "database_connections" {
  count = var.create_cloudwatch_alarms ? 1 : 0

  alarm_name          = "${local.name}-high-connections"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "DatabaseConnections"
  namespace           = "AWS/RDS"
  period              = "300"
  statistic           = "Average"
  threshold           = var.max_connections * 0.8 # 80% of max connections
  alarm_description   = "This metric monitors RDS database connections"
  alarm_actions       = var.alarm_actions

  dimensions = {
    DBInstanceIdentifier = aws_db_instance.main.id
  }

  tags = local.common_tags
}
