# Terraform Infrastructure - File Structure

Complete directory structure for Athena platform Terraform infrastructure.

## Overview

```
terraform/
├── README.md                           # Main documentation
├── DEPLOYMENT_GUIDE.md                 # Step-by-step deployment guide
├── ARCHITECTURE_SUMMARY.md             # Architecture decisions and comparisons
├── FILE_STRUCTURE.md                   # This file
├── Makefile                            # Convenient make targets
│
├── backend.tf                          # Remote state backend configuration
├── providers.tf                        # AWS provider configuration
├── variables.tf                        # Global variables
│
├── modules/                            # Reusable Terraform modules
│   ├── networking/                     # VPC, subnets, security groups
│   │   ├── main.tf                     # VPC, subnets, NAT, security groups
│   │   ├── variables.tf                # Input variables
│   │   └── outputs.tf                  # Output values
│   │
│   ├── eks/                            # Amazon EKS cluster
│   │   ├── main.tf                     # EKS cluster, node groups, IRSA
│   │   ├── variables.tf                # Input variables
│   │   └── outputs.tf                  # Output values
│   │
│   ├── rds/                            # PostgreSQL database
│   │   ├── main.tf                     # RDS instance, parameter group
│   │   ├── variables.tf                # Input variables
│   │   └── outputs.tf                  # Output values
│   │
│   ├── elasticache/                    # Redis cluster
│   │   ├── main.tf                     # ElastiCache replication group
│   │   ├── variables.tf                # Input variables
│   │   └── outputs.tf                  # Output values
│   │
│   ├── efs/                            # Elastic File System
│   │   ├── main.tf                     # EFS file system, access points
│   │   ├── variables.tf                # Input variables
│   │   └── outputs.tf                  # Output values
│   │
│   ├── s3/                             # Object storage & CDN
│   │   ├── main.tf                     # S3 bucket, CloudFront distribution
│   │   ├── variables.tf                # Input variables
│   │   └── outputs.tf                  # Output values
│   │
│   ├── secrets/                        # AWS Secrets Manager (TODO)
│   ├── dns/                            # Route53 & ACM (TODO)
│   ├── monitoring/                     # CloudWatch, Prometheus (TODO)
│   └── security/                       # WAF, GuardDuty (TODO)
│
├── environments/                       # Environment-specific configurations
│   ├── production/                     # Production environment
│   │   ├── main.tf                     # Main configuration
│   │   ├── variables.tf                # Variable definitions
│   │   ├── outputs.tf                  # Output definitions
│   │   ├── terraform.tfvars.example    # Example variable values
│   │   └── backend.hcl.example         # Example backend config
│   │
│   ├── staging/                        # Staging environment (TODO)
│   └── dev/                            # Development environment (TODO)
│
└── scripts/                            # Helper scripts
    ├── bootstrap-backend.sh            # Bootstrap S3/DynamoDB backend
    └── deploy-k8s.sh                   # Deploy Kubernetes manifests
```

## File Descriptions

### Root Level Files

#### README.md

- Main documentation for Terraform infrastructure
- Architecture overview
- Quick start guide
- Module descriptions
- Cost estimates
- Multi-cloud support

#### DEPLOYMENT_GUIDE.md

- Step-by-step deployment instructions
- Prerequisites and setup
- Detailed deployment steps
- Post-deployment configuration
- Monitoring setup
- Troubleshooting guide
- Disaster recovery procedures

#### ARCHITECTURE_SUMMARY.md

- Architecture decisions and rationale
- Cost comparisons (AWS vs GCP vs Azure)
- Security architecture
- Performance characteristics
- Disaster recovery strategy
- Future enhancements
- Migration guide

#### Makefile

- Convenient commands for common operations
- Examples:
  - `make bootstrap ENV=production`
  - `make plan ENV=production`
  - `make apply ENV=production`
  - `make deploy-k8s ENV=production`
  - `make outputs ENV=production`

#### backend.tf

- Terraform version requirements
- Provider version constraints
- Remote state backend configuration (S3 + DynamoDB)
- Commented out by default (configured per environment)

#### providers.tf

- AWS provider configuration
- Kubernetes provider configuration (connects to EKS)
- Helm provider configuration
- Default tags for all resources

#### variables.tf

- Global variables used across all environments
- Variable validation rules
- Default values
- Descriptions

### Modules

All modules follow the same structure:

- `main.tf`: Resource definitions
- `variables.tf`: Input variables
- `outputs.tf`: Output values

#### modules/networking/

Creates VPC and networking infrastructure:

- VPC with configurable CIDR
- Public subnets (for load balancers)
- Private subnets (for EKS nodes)
- Database subnets (for RDS, ElastiCache)
- Internet Gateway
- NAT Gateways (one per AZ or shared)
- Route tables and associations
- Security groups for all services
- VPC Flow Logs
- Network ACLs

Key Features:

- Multi-AZ support
- Single NAT Gateway option for cost savings
- Security groups with least privilege
- Tagged for EKS auto-discovery

#### modules/eks/

Creates Amazon EKS cluster:

- EKS cluster with configurable version
- Multiple managed node groups
- IAM roles and policies
- OIDC provider for IRSA
- EKS add-ons (VPC CNI, CoreDNS, kube-proxy)
- EBS CSI driver (for persistent volumes)
- EFS CSI driver (for shared storage)
- KMS encryption for secrets
- CloudWatch logging

Key Features:

- Mixed instance types and capacity types (On-Demand/Spot)
- Node labels and taints
- Auto-scaling configuration
- Pod security standards

#### modules/rds/

Creates PostgreSQL database:

- RDS PostgreSQL instance
- Multi-AZ deployment
- Automated backups
- KMS encryption
- Performance Insights
- Enhanced Monitoring
- Parameter group with tuned settings
- Subnet group
- CloudWatch alarms
- Secrets Manager integration

Key Features:

- Auto-generated passwords
- Storage autoscaling
- Optimized parameters for video platform
- Slow query logging

#### modules/elasticache/

Creates Redis cluster:

- ElastiCache replication group
- Multi-AZ with automatic failover
- Encryption at rest and in transit
- Auth token authentication
- Parameter group
- Subnet group
- CloudWatch alarms
- Secrets Manager integration

Key Features:

- Auto-generated auth tokens
- Configurable eviction policies
- Slow log and engine log to CloudWatch

#### modules/efs/

Creates Elastic File System:

- EFS file system
- Mount targets in each AZ
- KMS encryption
- Access points for different workloads
- Lifecycle policies (Infrequent Access)
- AWS Backup integration
- CloudWatch alarms

Key Features:

- ReadWriteMany access mode
- POSIX permissions
- Bursting or provisioned throughput
- Access points for storage and quarantine

#### modules/s3/

Creates object storage and CDN:

- S3 bucket for video storage
- Server-side encryption (KMS or AES256)
- Versioning
- Lifecycle policies (IA, Glacier, expiration)
- CORS configuration
- CloudFront distribution
- Origin Access Control
- Cache behaviors for different file types
- Geo-restriction support

Key Features:

- CloudFront for global delivery
- Custom domain support (ACM certificate)
- Intelligent caching by file type
- Public access blocked

### Environments

#### environments/production/

Production environment configuration:

- Uses all modules
- Multi-AZ deployment
- High availability settings
- 30-day backup retention
- Enhanced monitoring
- Large instance types

Key Features:

- Deletion protection enabled
- Encrypted everything
- CloudWatch alarms
- IRSA for S3 and Secrets Manager access

#### environments/staging/ (TODO)

Staging environment:

- Similar to production but smaller
- Single-AZ option
- Shorter backup retention
- Smaller instance types
- Cost-optimized

#### environments/dev/ (TODO)

Development environment:

- Minimal configuration
- Single-AZ
- t3.medium instances
- No Multi-AZ for databases
- Reduced retention periods
- Cost-optimized

### Scripts

#### scripts/bootstrap-backend.sh

Bootstraps Terraform remote state backend:

- Creates S3 bucket for state storage
- Enables versioning and encryption
- Blocks public access
- Creates DynamoDB table for state locking
- Generates backend.hcl configuration file

Usage:

```bash
./bootstrap-backend.sh production us-east-1
```

#### scripts/deploy-k8s.sh

Deploys Kubernetes manifests after Terraform:

- Retrieves Terraform outputs
- Configures kubectl
- Creates Kubernetes namespace
- Fetches secrets from AWS Secrets Manager
- Creates Kubernetes secrets
- Sets up ServiceAccounts with IRSA
- Creates EFS StorageClass and PVs/PVCs
- Creates ConfigMap
- Deploys application manifests
- Deploys monitoring stack

Usage:

```bash
./deploy-k8s.sh production
```

## Module Dependencies

```
networking
    │
    ├── eks (requires VPC, subnets, security groups)
    ├── rds (requires database subnets, security group)
    ├── elasticache (requires database subnets, security group)
    └── efs (requires private subnets, security group)

eks
    │
    └── (used by IRSA roles in main.tf)

s3
    │
    └── (independent, but IRSA role references it)
```

## Variable Flow

1. **Global variables** (terraform/variables.tf)
   - Default values for all environments
   - Used as fallbacks

2. **Environment variables** (environments/*/terraform.tfvars)
   - Override global defaults
   - Environment-specific values

3. **Module variables** (modules/*/variables.tf)
   - Receive values from environment configuration
   - Pass to resources

## Output Flow

1. **Module outputs** (modules/*/outputs.tf)
   - Export resource attributes
   - Used by other modules or environment

2. **Environment outputs** (environments/*/outputs.tf)
   - Aggregate module outputs
   - Provide connection information
   - Used by scripts

## Typical Workflow

1. **Bootstrap** (one-time)

   ```bash
   make bootstrap ENV=production REGION=us-east-1
   ```

2. **Configure**

   ```bash
   cp environments/production/terraform.tfvars.example environments/production/terraform.tfvars
   # Edit terraform.tfvars
   ```

3. **Initialize**

   ```bash
   make init ENV=production
   ```

4. **Plan**

   ```bash
   make plan ENV=production
   ```

5. **Apply**

   ```bash
   make apply ENV=production
   ```

6. **Deploy K8s**

   ```bash
   make deploy-k8s ENV=production
   ```

7. **Get Outputs**

   ```bash
   make outputs ENV=production
   ```

## Resource Count

Total resources created in production environment:

| Module | Resources |
|--------|-----------|
| networking | ~25 |
| eks | ~30 |
| rds | ~10 |
| elasticache | ~8 |
| efs | ~8 |
| s3 | ~10 |
| main.tf (IRSA) | ~4 |
| **Total** | **~95** |

## State Management

- **Backend**: S3 bucket with versioning
- **Locking**: DynamoDB table
- **Encryption**: AES256 for state file
- **Isolation**: Separate state per environment

State file locations:

- Production: `s3://athena-terraform-state-production/production/terraform.tfstate`
- Staging: `s3://athena-terraform-state-staging/staging/terraform.tfstate`
- Dev: `s3://athena-terraform-state-dev/dev/terraform.tfstate`

## Security

All modules follow security best practices:

- Encryption at rest (KMS)
- Encryption in transit (TLS 1.3)
- Least privilege IAM policies
- Private subnets for workloads
- Security groups with minimal access
- Secrets in Secrets Manager
- No hardcoded credentials
- CloudTrail logging (recommended to enable separately)

## Cost Tracking

All resources are tagged with:

- `Project`: athena
- `Environment`: production/staging/dev
- `ManagedBy`: Terraform
- `Owner`: (from variables)
- `CostCenter`: (from variables)

Use these tags in AWS Cost Explorer for cost allocation.

## Future Enhancements

Modules to be added:

- `modules/secrets/`: Centralized secrets management
- `modules/dns/`: Route53 hosted zone and ACM certificates
- `modules/monitoring/`: CloudWatch dashboards, Prometheus, Grafana
- `modules/security/`: WAF rules, GuardDuty, Security Hub

Environments to be added:

- `environments/staging/`: Staging environment
- `environments/dev/`: Development environment

Features to be added:

- GitOps integration (ArgoCD)
- Service mesh (Istio)
- CI/CD pipeline
- Automated testing
- Disaster recovery automation

## Additional Resources

- [Terraform AWS Provider Documentation](https://registry.terraform.io/providers/hashicorp/aws/latest/docs)
- [EKS Best Practices Guide](https://aws.github.io/aws-eks-best-practices/)
- [AWS Well-Architected Framework](https://aws.amazon.com/architecture/well-architected/)
- [Terraform Best Practices](https://www.terraform-best-practices.com/)

## Support

For questions or issues:

1. Check DEPLOYMENT_GUIDE.md troubleshooting section
2. Review CloudWatch logs
3. Check Terraform state: `make state-list ENV=production`
4. Open an issue on GitHub
