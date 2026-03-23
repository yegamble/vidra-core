variable "project_name" {
  description = "Project name for resource naming"
  type        = string
}

variable "environment" {
  description = "Environment name"
  type        = string
}

variable "subnet_ids" {
  description = "List of subnet IDs for DB subnet group"
  type        = list(string)
}

variable "security_group_id" {
  description = "Security group ID for RDS instance"
  type        = string
}

variable "engine_version" {
  description = "PostgreSQL engine version"
  type        = string
  default     = "15.4"
}

variable "instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.r6g.xlarge"
}

variable "allocated_storage" {
  description = "Initial allocated storage in GB"
  type        = number
  default     = 100
}

variable "max_allocated_storage" {
  description = "Maximum storage for autoscaling in GB"
  type        = number
  default     = 1000
}

variable "storage_type" {
  description = "Storage type (gp3, gp2, io1, io2)"
  type        = string
  default     = "gp3"
}

variable "iops" {
  description = "IOPS for io1/io2 storage"
  type        = number
  default     = null
}

variable "database_name" {
  description = "Name of the database to create"
  type        = string
  default     = "vidra"
}

variable "master_username" {
  description = "Master username for the database"
  type        = string
  default     = "postgres"
}

variable "master_password" {
  description = "Master password (if null, will be auto-generated)"
  type        = string
  default     = null
  sensitive   = true
}

variable "port" {
  description = "Database port"
  type        = number
  default     = 5432
}

variable "multi_az" {
  description = "Enable Multi-AZ deployment"
  type        = bool
  default     = true
}

variable "backup_retention_period" {
  description = "Backup retention period in days"
  type        = number
  default     = 7
}

variable "backup_window" {
  description = "Preferred backup window"
  type        = string
  default     = "03:00-04:00"
}

variable "maintenance_window" {
  description = "Preferred maintenance window"
  type        = string
  default     = "sun:04:00-sun:05:00"
}

variable "skip_final_snapshot" {
  description = "Skip final snapshot when destroying"
  type        = bool
  default     = false
}

variable "deletion_protection" {
  description = "Enable deletion protection"
  type        = bool
  default     = true
}

variable "enable_encryption" {
  description = "Enable storage encryption"
  type        = bool
  default     = true
}

variable "auto_minor_version_upgrade" {
  description = "Enable automatic minor version upgrades"
  type        = bool
  default     = true
}

variable "apply_immediately" {
  description = "Apply changes immediately"
  type        = bool
  default     = false
}

variable "enable_performance_insights" {
  description = "Enable Performance Insights"
  type        = bool
  default     = true
}

variable "performance_insights_retention_period" {
  description = "Performance Insights retention period in days"
  type        = number
  default     = 7
}

variable "monitoring_interval" {
  description = "Enhanced monitoring interval in seconds (0, 1, 5, 10, 15, 30, 60)"
  type        = number
  default     = 60
}

variable "parameter_group_family" {
  description = "DB parameter group family"
  type        = string
  default     = "postgres15"
}

# Performance tuning parameters
variable "max_connections" {
  description = "Maximum number of database connections"
  type        = string
  default     = "200"
}

variable "shared_buffers" {
  description = "Shared buffers (1/4 of instance memory recommended)"
  type        = string
  default     = "2097152" # 2GB in KB for xlarge instances
}

variable "effective_cache_size" {
  description = "Effective cache size (3/4 of instance memory recommended)"
  type        = string
  default     = "6291456" # 6GB in KB for xlarge instances
}

variable "maintenance_work_mem" {
  description = "Maintenance work memory"
  type        = string
  default     = "524288" # 512MB in KB
}

variable "log_min_duration_statement" {
  description = "Log statements taking longer than this (ms), -1 to disable"
  type        = string
  default     = "1000" # Log slow queries (>1s)
}

variable "create_cloudwatch_alarms" {
  description = "Create CloudWatch alarms for RDS"
  type        = bool
  default     = true
}

variable "alarm_actions" {
  description = "List of ARNs to notify when alarm triggers"
  type        = list(string)
  default     = []
}

variable "tags" {
  description = "Additional tags"
  type        = map(string)
  default     = {}
}
