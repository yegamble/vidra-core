# Vidra Core Video Platform - Terraform Infrastructure

This directory contains Infrastructure as Code (IaC) for deploying the Vidra Core video platform to AWS.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      Route53 + ACM (SSL)                     │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│              Application Load Balancer (ALB)                 │
│           (Managed by Kubernetes Ingress Controller)         │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                   Amazon EKS Cluster                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  API Pods    │  │   Encoding   │  │    IPFS      │      │
│  │  (3-20)      │  │   Workers    │  │   ClamAV     │      │
│  │              │  │   (2-10)     │  │              │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
           │                    │                    │
           │                    │                    │
           ▼                    ▼                    ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   RDS Postgres  │  │  ElastiCache    │  │   EFS (500GB)   │
│   (Multi-AZ)    │  │     Redis       │  │  ReadWriteMany  │
└─────────────────┘  └─────────────────┘  └─────────────────┘
                                                    │
                                                    ▼
                                          ┌─────────────────┐
                                          │   S3 Bucket     │
                                          │  + CloudFront   │
                                          │  (Video CDN)    │
                                          └─────────────────┘
```

## Directory Structure

```
terraform/
├── modules/                    # Reusable Terraform modules
│   ├── networking/            # VPC, subnets, NAT gateways, security groups
│   ├── eks/                   # EKS cluster, node groups, IRSA
│   ├── rds/                   # PostgreSQL database
│   ├── elasticache/           # Redis cluster
│   ├── s3/                    # Object storage + CloudFront CDN
│   ├── efs/                   # Elastic File System for shared storage
│   ├── secrets/               # AWS Secrets Manager
│   ├── dns/                   # Route53 hosted zone + ACM certificates
│   ├── monitoring/            # CloudWatch, Prometheus operator
│   └── security/              # IAM roles, KMS keys, security policies
│
├── environments/              # Environment-specific configurations
│   ├── dev/                  # Development environment
│   ├── staging/              # Staging environment
│   └── production/           # Production environment
│
├── backend.tf                # Remote state backend (S3 + DynamoDB)
├── providers.tf              # AWS provider configuration
├── terraform.tfvars.example  # Example variables file
└── README.md                 # This file
```

## Prerequisites

1. **AWS CLI** configured with appropriate credentials

   ```bash
   aws configure
   ```

2. **Terraform** >= 1.6.0

   ```bash
   terraform version
   ```

3. **kubectl** for Kubernetes management

   ```bash
   kubectl version --client
   ```

4. **helm** for installing Kubernetes operators

   ```bash
   helm version
   ```

## Quick Start

### 1. Bootstrap Remote State Backend

First-time setup requires creating the S3 bucket and DynamoDB table for state management:

```bash
cd terraform
./scripts/bootstrap-backend.sh production us-east-1
```

### 2. Initialize Terraform

```bash
cd environments/production
terraform init
```

### 3. Configure Variables

```bash
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your specific values
```

### 4. Plan Infrastructure

```bash
terraform plan -out=tfplan
```

### 5. Apply Infrastructure

```bash
terraform apply tfplan
```

### 6. Configure kubectl

```bash
aws eks update-kubeconfig --region us-east-1 --name vidra-production-eks
```

### 7. Deploy Kubernetes Manifests

```bash
# Apply existing K8s manifests
kubectl apply -k ../../k8s/base/
kubectl apply -k ../../k8s/monitoring/
```

## Environment Management

### Development Environment

- Single-AZ deployment
- Smaller instance types (t3.medium)
- No Multi-AZ for RDS/ElastiCache
- Lower min/max replicas

```bash
cd environments/dev
terraform apply
```

### Staging Environment

- Multi-AZ deployment
- Production-like configuration
- Reduced capacity

```bash
cd environments/staging
terraform apply
```

### Production Environment

- Full Multi-AZ deployment
- High availability configuration
- Auto-scaling enabled
- Enhanced monitoring

```bash
cd environments/production
terraform apply
```

## Cost Optimization Strategies

### 1. Spot Instances for Encoding Workers

Encoding workers are configured to use EC2 Spot instances (70% cost reduction):

- Tolerates interruptions
- Graceful shutdown handling
- Queue-based workload

### 2. Graviton2 Instances

API pods run on ARM-based Graviton2 instances (20% cost savings):

- Better performance per dollar
- Lower power consumption

### 3. RDS Storage Autoscaling

PostgreSQL storage automatically scales:

- Start with minimal capacity
- Grows as needed
- Max limit prevents runaway costs

### 4. S3 Lifecycle Policies

Automated data tiering:

- Infrequent Access after 30 days
- Glacier after 90 days
- Delete after 365 days (configurable)

### 5. ElastiCache Reserved Nodes

1-year or 3-year commitments for 30-50% savings on Redis cache.

### 6. EKS Fargate for Auxiliary Services

IPFS and ClamAV run on Fargate (pay-per-pod):

- No EC2 overhead
- Auto-scaling without node management

## Security Best Practices

### Network Security

- Private subnets for all workloads
- NAT Gateways for egress
- Security groups with least privilege
- Network ACLs for additional defense

### Encryption

- EBS volumes encrypted (KMS)
- RDS encryption at rest (KMS)
- S3 server-side encryption
- Secrets Manager for sensitive data
- TLS 1.3 for all traffic

### IAM & RBAC

- IRSA (IAM Roles for Service Accounts)
- Least privilege IAM policies
- Kubernetes RBAC
- Pod Security Standards

### Monitoring & Auditing

- CloudWatch Logs aggregation
- CloudTrail for API auditing
- VPC Flow Logs
- Prometheus metrics
- Grafana dashboards

## State Management

State is stored remotely in S3 with DynamoDB locking:

```hcl
terraform {
  backend "s3" {
    bucket         = "vidra-terraform-state-production"
    key            = "production/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "vidra-terraform-locks"
  }
}
```

## Module Outputs

After applying, retrieve important outputs:

```bash
terraform output eks_cluster_endpoint
terraform output rds_endpoint
terraform output redis_endpoint
terraform output s3_bucket_name
terraform output cloudfront_distribution_domain
```

## Disaster Recovery

### Backup Strategy

- RDS automated backups (7-day retention)
- RDS snapshots before changes
- S3 versioning enabled
- EFS automated backups

### Recovery Procedure

1. Restore RDS from snapshot
2. Recover S3 objects from versioning
3. Redeploy EKS cluster from Terraform
4. Restore EFS from backup

## Troubleshooting

### EKS Node Group Fails to Launch

Check security groups and ensure EC2 instances can reach EKS control plane.

### RDS Connection Issues

Verify security groups allow traffic from EKS nodes on port 5432.

### S3 Upload Failures

Check IAM roles for service accounts (IRSA) configuration.

### High Costs

Review CloudWatch cost allocation tags and check for:

- Over-provisioned instances
- Unused EBS volumes
- Excessive data transfer

## Multi-Cloud Support

While AWS is recommended, the modules are designed to be cloud-agnostic with minor modifications:

### Google Cloud Platform (GCP)

- Replace `modules/eks` with `modules/gke`
- Replace `modules/rds` with `modules/cloud-sql`
- Replace `modules/elasticache` with `modules/memorystore`
- Replace `modules/efs` with `modules/filestore`

### Azure

- Replace `modules/eks` with `modules/aks`
- Replace `modules/rds` with `modules/azure-database`
- Replace `modules/elasticache` with `modules/azure-cache`
- Replace `modules/efs` with `modules/azure-files`

## Support & Contributions

For issues or questions:

1. Check existing Terraform state: `terraform show`
2. Review CloudWatch logs
3. Validate with `terraform validate`
4. Test with `terraform plan`

## License

Same as parent project (see root LICENSE file).
