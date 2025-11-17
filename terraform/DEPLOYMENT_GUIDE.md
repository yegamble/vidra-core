# Athena Platform - Terraform Deployment Guide

Complete guide for deploying Athena video platform infrastructure using Terraform.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Architecture Overview](#architecture-overview)
3. [Cost Estimation](#cost-estimation)
4. [Deployment Steps](#deployment-steps)
5. [Post-Deployment Configuration](#post-deployment-configuration)
6. [Monitoring and Maintenance](#monitoring-and-maintenance)
7. [Disaster Recovery](#disaster-recovery)
8. [Troubleshooting](#troubleshooting)

## Prerequisites

### Required Tools

Install the following tools on your local machine:

```bash
# AWS CLI
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

# Terraform >= 1.6.0
wget https://releases.hashicorp.com/terraform/1.6.6/terraform_1.6.6_linux_amd64.zip
unzip terraform_1.6.6_linux_amd64.zip
sudo mv terraform /usr/local/bin/

# kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
chmod +x kubectl
sudo mv kubectl /usr/local/bin/

# helm
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# jq (for JSON parsing in scripts)
sudo apt-get install -y jq  # Debian/Ubuntu
# or
sudo yum install -y jq      # RHEL/CentOS
```

### AWS Account Setup

1. **Create AWS Account** or use existing account
2. **Configure AWS credentials**:
   ```bash
   aws configure
   # Enter your AWS Access Key ID, Secret Access Key, default region, and output format
   ```

3. **Verify credentials**:
   ```bash
   aws sts get-caller-identity
   ```

4. **Required IAM permissions**: Your user/role needs permissions for:
   - VPC, Subnets, Security Groups, NAT Gateways
   - EKS, EC2, Auto Scaling
   - RDS, ElastiCache, EFS, S3
   - IAM roles and policies
   - KMS keys
   - CloudWatch logs and alarms
   - Route53, ACM (if using custom domain)
   - Secrets Manager

### Domain Setup (Optional but Recommended)

1. **Register a domain** or use existing domain
2. **Create Route53 hosted zone**:
   ```bash
   aws route53 create-hosted-zone --name athena.example.com --caller-reference $(date +%s)
   ```

3. **Request ACM certificate** in us-east-1 (for CloudFront):
   ```bash
   aws acm request-certificate \
     --domain-name "*.athena.example.com" \
     --subject-alternative-names "athena.example.com" \
     --validation-method DNS \
     --region us-east-1
   ```

4. **Validate certificate** by adding DNS records shown in ACM console

## Architecture Overview

### Infrastructure Components

```
┌─────────────────────────────────────────────────────────────┐
│                         AWS Cloud                            │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │                  VPC (10.0.0.0/16)                  │    │
│  │                                                      │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐ │    │
│  │  │   Public    │  │   Public    │  │   Public    │ │    │
│  │  │  Subnet 1   │  │  Subnet 2   │  │  Subnet 3   │ │    │
│  │  │  (NAT GW)   │  │  (NAT GW)   │  │  (NAT GW)   │ │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘ │    │
│  │         │                 │                 │        │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐ │    │
│  │  │   Private   │  │   Private   │  │   Private   │ │    │
│  │  │  Subnet 1   │  │  Subnet 2   │  │  Subnet 3   │ │    │
│  │  │ (EKS Nodes) │  │ (EKS Nodes) │  │ (EKS Nodes) │ │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘ │    │
│  │         │                 │                 │        │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐ │    │
│  │  │  Database   │  │  Database   │  │  Database   │ │    │
│  │  │  Subnet 1   │  │  Subnet 2   │  │  Subnet 3   │ │    │
│  │  │ (RDS,Redis) │  │ (RDS,Redis) │  │ (RDS,Redis) │ │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘ │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
│  ┌────────────────────────────────────────────────────┐    │
│  │                   EKS Cluster                       │    │
│  │  - API Pods (3-20 replicas, t3.xlarge)             │    │
│  │  - Encoding Workers (2-10 replicas, c6i.4xlarge)   │    │
│  │  - System Services (IPFS, ClamAV)                  │    │
│  └────────────────────────────────────────────────────┘    │
│                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │RDS PostgreSQL│  │ElastiCache   │  │     EFS      │     │
│  │  (Multi-AZ)  │  │    Redis     │  │  (500GB+)    │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
│                                                              │
│  ┌──────────────────────────────────────────────────┐      │
│  │              S3 + CloudFront CDN                  │      │
│  │  - Video storage with lifecycle policies          │      │
│  │  - Global content delivery                        │      │
│  └──────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

### Resource Naming Convention

All resources follow the pattern: `{project}-{environment}-{resource}`

Example:
- VPC: `athena-production-vpc`
- EKS: `athena-production-eks`
- RDS: `athena-production-postgres`
- S3: `athena-production-videos-{account-id}`

## Cost Estimation

### Production Environment (Monthly)

| Service | Configuration | Cost |
|---------|--------------|------|
| EKS Control Plane | 1 cluster | $73 |
| EC2 - API Nodes | 3x t3.xlarge (on-demand) | $450 |
| EC2 - Encoding Workers | 2x c6i.4xlarge (spot) | $300 |
| RDS PostgreSQL | db.r6g.xlarge Multi-AZ | $520 |
| ElastiCache Redis | cache.r6g.large x2 | $180 |
| EFS | 500GB | $150 |
| S3 Storage | 1TB | $23 |
| CloudFront | 5TB transfer | $422 |
| NAT Gateways | 3x $0.045/hour | $97 |
| Data Transfer | Estimate | $50 |
| **Total** | | **~$2,265/month** |

### Cost Optimization Strategies

1. **Use Reserved Instances** for API nodes (save 30-50%)
2. **Spot Instances** for encoding workers (save 70%)
3. **S3 Lifecycle Policies** to move old videos to cheaper storage
4. **Right-size instances** based on actual usage
5. **Single NAT Gateway** for dev/staging (not recommended for production)

### Development Environment (Monthly)

Much smaller footprint: **~$400-600/month**
- Smaller instances (t3.medium)
- Single-AZ deployment
- No Multi-AZ for databases
- Reduced min/max replicas

## Deployment Steps

### Step 1: Bootstrap Terraform Backend

Create S3 bucket and DynamoDB table for Terraform state:

```bash
cd terraform/scripts
./bootstrap-backend.sh production us-east-1
```

This creates:
- S3 bucket: `athena-terraform-state-production`
- DynamoDB table: `athena-terraform-locks`
- Backend config file: `environments/production/backend.hcl`

### Step 2: Configure Variables

```bash
cd ../environments/production
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars` with your specific values:

```hcl
project_name = "athena"
environment  = "production"
aws_region   = "us-east-1"
owner_email  = "devops@yourcompany.com"

domain_name = "athena.yourcompany.com"

# Update CloudFront configuration if using custom domain
cloudfront_aliases = ["cdn.athena.yourcompany.com"]
acm_certificate_arn = "arn:aws:acm:us-east-1:ACCOUNT:certificate/CERT-ID"

# Security: Restrict access to your IP ranges
allowed_cidr_blocks = [
  "203.0.113.0/24",  # Your office IP range
  "198.51.100.0/24"  # VPN IP range
]
```

### Step 3: Initialize Terraform

```bash
terraform init -backend-config=backend.hcl
```

Expected output:
```
Initializing modules...
Initializing the backend...
Successfully configured the backend "s3"!
```

### Step 4: Plan Infrastructure

Review what will be created:

```bash
terraform plan -out=tfplan
```

Review the output carefully. You should see:
- ~80-100 resources to be created
- VPC with 9 subnets (3 public, 3 private, 3 database)
- EKS cluster with node groups
- RDS PostgreSQL instance
- ElastiCache Redis cluster
- EFS file system
- S3 bucket with CloudFront distribution
- IAM roles and policies
- Security groups
- KMS keys

### Step 5: Apply Infrastructure

Deploy the infrastructure:

```bash
terraform apply tfplan
```

This will take approximately **30-40 minutes** to complete.

Breakdown:
- VPC and networking: 2-3 minutes
- EKS cluster: 15-20 minutes
- RDS database: 10-15 minutes
- ElastiCache: 5-7 minutes
- EFS, S3, IAM: 2-3 minutes

### Step 6: Retrieve Outputs

After successful deployment:

```bash
terraform output
```

Important outputs:
```
eks_cluster_name = "athena-production-eks"
rds_endpoint = "athena-production-postgres.xxxxx.us-east-1.rds.amazonaws.com"
redis_endpoint = "athena-production-redis.xxxxx.cache.amazonaws.com"
s3_bucket_name = "athena-production-videos-123456789012"
cloudfront_domain_name = "d1234567890abc.cloudfront.net"
```

Save these for later use.

### Step 7: Configure kubectl

```bash
aws eks update-kubeconfig --region us-east-1 --name athena-production-eks
kubectl get nodes
```

You should see your EKS nodes:
```
NAME                          STATUS   ROLES    AGE   VERSION
ip-10-0-1-123.ec2.internal    Ready    <none>   5m    v1.28.x
ip-10-0-2-234.ec2.internal    Ready    <none>   5m    v1.28.x
ip-10-0-3-345.ec2.internal    Ready    <none>   5m    v1.28.x
```

### Step 8: Deploy Kubernetes Applications

Use the deployment script:

```bash
cd ../../scripts
./deploy-k8s.sh production
```

This script:
1. Retrieves Terraform outputs
2. Configures kubectl
3. Creates Kubernetes namespace
4. Fetches secrets from AWS Secrets Manager
5. Creates Kubernetes secrets
6. Sets up ServiceAccounts with IRSA
7. Creates EFS StorageClass and PVs
8. Deploys application manifests
9. Deploys monitoring stack

### Step 9: Verify Deployment

Check pod status:

```bash
kubectl get pods -n athena-production
```

Expected output:
```
NAME                                    READY   STATUS    RESTARTS   AGE
athena-api-xxxxx                        1/1     Running   0          2m
athena-api-yyyyy                        1/1     Running   0          2m
athena-api-zzzzz                        1/1     Running   0          2m
athena-encoding-worker-aaaaa            1/1     Running   0          2m
athena-encoding-worker-bbbbb            1/1     Running   0          2m
```

Check services:

```bash
kubectl get svc -n athena-production
```

Check ingress:

```bash
kubectl get ingress -n athena-production
```

## Post-Deployment Configuration

### 1. Configure DNS

If using custom domain, create DNS records:

```bash
# Get ingress load balancer
INGRESS_LB=$(kubectl get ingress athena-ingress -n athena-production -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')

# Create Route53 record (example)
aws route53 change-resource-record-sets --hosted-zone-id ZXXXXX --change-batch '{
  "Changes": [{
    "Action": "CREATE",
    "ResourceRecordSet": {
      "Name": "athena.yourcompany.com",
      "Type": "CNAME",
      "TTL": 300,
      "ResourceRecords": [{"Value": "'"$INGRESS_LB"'"}]
    }
  }]
}'
```

### 2. Configure CloudFront Custom Domain

If using custom domain for CDN:

```bash
# Get CloudFront distribution domain
CDN_DOMAIN=$(terraform output -raw cloudfront_domain_name)

# Create Route53 record for CDN
aws route53 change-resource-record-sets --hosted-zone-id ZXXXXX --change-batch '{
  "Changes": [{
    "Action": "CREATE",
    "ResourceRecordSet": {
      "Name": "cdn.athena.yourcompany.com",
      "Type": "A",
      "AliasTarget": {
        "HostedZoneId": "Z2FDTNDATAQYW2",
        "DNSName": "'"$CDN_DOMAIN"'",
        "EvaluateTargetHealth": false
      }
    }
  }]
}'
```

### 3. Database Migrations

Run database migrations:

```bash
# Get a pod name
POD=$(kubectl get pod -n athena-production -l app=athena,component=api -o jsonpath='{.items[0].metadata.name}')

# Run migrations
kubectl exec -n athena-production $POD -- /app/athena migrate up
```

### 4. Set Up Monitoring

Access Grafana:

```bash
kubectl port-forward -n athena-production svc/grafana 3000:80
```

Navigate to http://localhost:3000
- Default credentials: admin/admin (change immediately)
- Import dashboards from `k8s/monitoring/dashboards/`

### 5. Configure Alerting

Create SNS topic for alerts:

```bash
aws sns create-topic --name athena-production-alerts
aws sns subscribe --topic-arn arn:aws:sns:us-east-1:ACCOUNT:athena-production-alerts \
  --protocol email --notification-endpoint devops@yourcompany.com
```

Update Terraform variables:

```hcl
# In terraform.tfvars
alarm_actions = ["arn:aws:sns:us-east-1:ACCOUNT:athena-production-alerts"]
```

Apply changes:

```bash
terraform apply
```

## Monitoring and Maintenance

### Key Metrics to Monitor

1. **EKS Cluster**
   - Node CPU/Memory utilization
   - Pod count and status
   - HPA scaling events

2. **RDS PostgreSQL**
   - CPU utilization
   - Database connections
   - Free storage space
   - Read/Write IOPS

3. **ElastiCache Redis**
   - CPU utilization
   - Memory usage
   - Evictions
   - Cache hit ratio

4. **EFS**
   - Burst credit balance
   - I/O limit percentage

5. **Application**
   - Request rate
   - Error rate
   - Response time (p50, p95, p99)
   - Video encoding queue depth

### Regular Maintenance Tasks

#### Weekly
- Review CloudWatch alarms
- Check pod logs for errors
- Review cost reports
- Verify backups are running

#### Monthly
- Update Kubernetes manifests
- Review and optimize instance types
- Clean up old EBS snapshots
- Review security group rules

#### Quarterly
- Update Terraform modules
- Review IAM policies
- Update EKS cluster version
- Review and update monitoring dashboards

## Disaster Recovery

### Backup Strategy

1. **RDS Automated Backups**
   - Daily automated backups (30-day retention)
   - Point-in-time recovery
   - Manual snapshots before major changes

2. **EFS Automated Backups**
   - AWS Backup integration
   - Daily backups with 30-day retention

3. **S3 Versioning**
   - Enabled on video bucket
   - Can recover from accidental deletions

4. **Terraform State**
   - Versioned in S3
   - DynamoDB state locking prevents corruption

### Recovery Procedures

#### Recover RDS from Snapshot

```bash
aws rds restore-db-instance-from-db-snapshot \
  --db-instance-identifier athena-production-postgres-recovered \
  --db-snapshot-identifier athena-production-postgres-snapshot-YYYY-MM-DD
```

#### Recover EFS from Backup

```bash
aws backup start-restore-job \
  --recovery-point-arn arn:aws:backup:REGION:ACCOUNT:recovery-point:XXXXX \
  --iam-role-arn arn:aws:iam::ACCOUNT:role/service-role/AWSBackupDefaultServiceRole
```

#### Recover Entire Infrastructure

```bash
cd terraform/environments/production
terraform init -backend-config=backend.hcl
terraform plan
terraform apply
```

## Troubleshooting

### Issue: Terraform Apply Fails

**Symptom**: Terraform apply fails with error
**Solution**:
```bash
# Check AWS credentials
aws sts get-caller-identity

# Verify region
aws configure get region

# Check quota limits
aws service-quotas list-service-quotas --service-code ec2

# Retry with verbose logging
export TF_LOG=DEBUG
terraform apply
```

### Issue: EKS Nodes Not Joining Cluster

**Symptom**: Nodes show in EC2 but not in `kubectl get nodes`
**Solution**:
```bash
# Check node instance profile has correct permissions
aws iam get-instance-profile --instance-profile-name athena-production-eks-*

# Check security groups allow node-to-cluster communication
aws ec2 describe-security-groups --group-ids sg-xxxxx

# Check node logs
aws ec2 get-console-output --instance-id i-xxxxx
```

### Issue: RDS Connection Timeout

**Symptom**: Pods can't connect to RDS
**Solution**:
```bash
# Verify security group allows traffic from EKS nodes
aws ec2 describe-security-groups --group-ids sg-rds

# Test connection from pod
kubectl run -it --rm debug --image=postgres:15 --restart=Never -- bash
psql -h athena-production-postgres.xxxxx.rds.amazonaws.com -U athenaadmin -d athena
```

### Issue: High Costs

**Symptom**: AWS bill higher than expected
**Solution**:
```bash
# Check for running resources
aws ec2 describe-instances --query 'Reservations[].Instances[?State.Name==`running`]'
aws rds describe-db-instances --query 'DBInstances[?DBInstanceStatus==`available`]'

# Review Cost Explorer
# Look for: NAT Gateway data processing, CloudFront data transfer, RDS IOPS

# Optimization actions:
# 1. Use single NAT Gateway for non-production
# 2. Enable S3 Transfer Acceleration instead of CloudFront for uploads
# 3. Use gp3 instead of io2 for RDS
# 4. Enable EFS lifecycle policies
```

### Issue: Pod Crashes or OOMKilled

**Symptom**: Pods restarting frequently
**Solution**:
```bash
# Check pod status and events
kubectl describe pod athena-api-xxxxx -n athena-production

# Check resource usage
kubectl top pod athena-api-xxxxx -n athena-production

# View logs
kubectl logs athena-api-xxxxx -n athena-production --previous

# Adjust resource limits in deployment
kubectl edit deployment athena-api -n athena-production
```

## Support and Contributing

For issues or questions:
1. Check this guide
2. Review CloudWatch logs
3. Check Kubernetes events
4. Review Terraform state
5. Open an issue on GitHub

## Security Considerations

1. **Rotate credentials regularly**
   ```bash
   # Rotate RDS password
   aws rds modify-db-instance --db-instance-identifier athena-production-postgres \
     --master-user-password NewSecurePassword123!

   # Update Kubernetes secret
   kubectl create secret generic athena-secrets --from-literal=database-url=... \
     --dry-run=client -o yaml | kubectl apply -f -
   ```

2. **Enable GuardDuty**
   ```bash
   aws guardduty create-detector --enable
   ```

3. **Enable CloudTrail**
   ```bash
   aws cloudtrail create-trail --name athena-production-trail \
     --s3-bucket-name athena-cloudtrail-logs
   ```

4. **Regular security audits**
   - Review IAM policies
   - Check security group rules
   - Update dependencies
   - Scan container images

---

**Last Updated**: 2025-01-17
**Terraform Version**: >= 1.6.0
**Kubernetes Version**: 1.28
