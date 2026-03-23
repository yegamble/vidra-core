#!/bin/bash
# Deploy Kubernetes manifests after Terraform infrastructure is ready
# Usage: ./deploy-k8s.sh <environment>
# Example: ./deploy-k8s.sh production

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check arguments
if [ $# -ne 1 ]; then
    echo -e "${RED}Error: Invalid number of arguments${NC}"
    echo "Usage: $0 <environment>"
    echo "Example: $0 production"
    exit 1
fi

ENVIRONMENT=$1
PROJECT_NAME="vidra"

echo -e "${GREEN}=== Kubernetes Deployment ===${NC}"
echo "Environment: $ENVIRONMENT"
echo ""

# Check if kubectl is installed
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}Error: kubectl is not installed${NC}"
    exit 1
fi

# Check if Terraform state exists
TF_DIR="../environments/${ENVIRONMENT}"
if [ ! -d "$TF_DIR" ]; then
    echo -e "${RED}Error: Environment directory not found: $TF_DIR${NC}"
    exit 1
fi

cd "$TF_DIR"

# Get outputs from Terraform
echo -e "${YELLOW}Retrieving Terraform outputs...${NC}"
EKS_CLUSTER_NAME=$(terraform output -raw eks_cluster_name)
AWS_REGION=$(terraform output -raw aws_region 2>/dev/null || echo "us-east-1")
RDS_SECRET_NAME=$(terraform output -raw rds_secret_name)
REDIS_SECRET_NAME=$(terraform output -raw redis_secret_name)
EFS_ID=$(terraform output -raw efs_id)
EFS_STORAGE_AP=$(terraform output -raw efs_storage_access_point_id)
EFS_QUARANTINE_AP=$(terraform output -raw efs_quarantine_access_point_id)
S3_BUCKET=$(terraform output -raw s3_bucket_name)
S3_ROLE_ARN=$(terraform output -raw s3_access_role_arn)
SECRETS_ROLE_ARN=$(terraform output -raw secrets_access_role_arn)

echo -e "${GREEN}✓ Terraform outputs retrieved${NC}"

# Configure kubectl
echo -e "${YELLOW}Configuring kubectl...${NC}"
aws eks update-kubeconfig --region "$AWS_REGION" --name "$EKS_CLUSTER_NAME"
echo -e "${GREEN}✓ kubectl configured${NC}"

# Create namespace if it doesn't exist
NAMESPACE="vidra-${ENVIRONMENT}"
echo -e "${YELLOW}Creating namespace: $NAMESPACE${NC}"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
echo -e "${GREEN}✓ Namespace ready${NC}"

# Create Kubernetes secrets from AWS Secrets Manager
echo -e "${YELLOW}Creating Kubernetes secrets from AWS Secrets Manager...${NC}"

# Get RDS credentials
RDS_SECRET=$(aws secretsmanager get-secret-value --secret-id "$RDS_SECRET_NAME" --region "$AWS_REGION" --query SecretString --output text)
DB_USERNAME=$(echo "$RDS_SECRET" | jq -r .username)
DB_PASSWORD=$(echo "$RDS_SECRET" | jq -r .password)
DB_HOST=$(echo "$RDS_SECRET" | jq -r .host)
DB_PORT=$(echo "$RDS_SECRET" | jq -r .port)
DB_NAME=$(echo "$RDS_SECRET" | jq -r .dbname)
DATABASE_URL="postgresql://${DB_USERNAME}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=require"

# Get Redis credentials
if [ "$REDIS_SECRET_NAME" != "null" ]; then
    REDIS_SECRET=$(aws secretsmanager get-secret-value --secret-id "$REDIS_SECRET_NAME" --region "$AWS_REGION" --query SecretString --output text)
    REDIS_AUTH_TOKEN=$(echo "$REDIS_SECRET" | jq -r .auth_token)
    REDIS_ENDPOINT=$(echo "$REDIS_SECRET" | jq -r .endpoint)
    REDIS_PORT=$(echo "$REDIS_SECRET" | jq -r .port)
    REDIS_URL="rediss://:${REDIS_AUTH_TOKEN}@${REDIS_ENDPOINT}:${REDIS_PORT}"
else
    REDIS_URL=$(terraform output -raw redis_connection_string)
fi

# Generate JWT secret
JWT_SECRET=$(openssl rand -base64 32)

# Create Kubernetes secret
kubectl create secret generic vidra-secrets \
    --from-literal=database-url="$DATABASE_URL" \
    --from-literal=redis-url="$REDIS_URL" \
    --from-literal=jwt-secret="$JWT_SECRET" \
    --namespace="$NAMESPACE" \
    --dry-run=client -o yaml | kubectl apply -f -

echo -e "${GREEN}✓ Secrets created${NC}"

# Create ServiceAccount with IRSA
echo -e "${YELLOW}Creating ServiceAccount with IAM roles...${NC}"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vidra-api
  namespace: $NAMESPACE
  annotations:
    eks.amazonaws.com/role-arn: $S3_ROLE_ARN
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vidra-secrets
  namespace: $NAMESPACE
  annotations:
    eks.amazonaws.com/role-arn: $SECRETS_ROLE_ARN
EOF
echo -e "${GREEN}✓ ServiceAccounts created${NC}"

# Create StorageClass for EFS
echo -e "${YELLOW}Creating EFS StorageClass...${NC}"
cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: efs-sc
provisioner: efs.csi.aws.com
parameters:
  provisioningMode: efs-ap
  fileSystemId: $EFS_ID
  directoryPerms: "755"
EOF
echo -e "${GREEN}✓ StorageClass created${NC}"

# Create PersistentVolumes and PersistentVolumeClaims for EFS
echo -e "${YELLOW}Creating PersistentVolumes for EFS...${NC}"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: vidra-storage-pv
spec:
  capacity:
    storage: 500Gi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  storageClassName: efs-sc
  csi:
    driver: efs.csi.aws.com
    volumeHandle: ${EFS_ID}::${EFS_STORAGE_AP}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: vidra-storage
  namespace: $NAMESPACE
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: efs-sc
  resources:
    requests:
      storage: 500Gi
  volumeName: vidra-storage-pv
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: vidra-quarantine-pv
spec:
  capacity:
    storage: 10Gi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  storageClassName: efs-sc
  csi:
    driver: efs.csi.aws.com
    volumeHandle: ${EFS_ID}::${EFS_QUARANTINE_AP}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: vidra-quarantine
  namespace: $NAMESPACE
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: efs-sc
  resources:
    requests:
      storage: 10Gi
  volumeName: vidra-quarantine-pv
EOF
echo -e "${GREEN}✓ PersistentVolumes created${NC}"

# Update ConfigMap with S3 configuration
echo -e "${YELLOW}Creating ConfigMap...${NC}"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: vidra-config
  namespace: $NAMESPACE
  labels:
    app: vidra
data:
  # S3 Configuration
  s3-bucket: "$S3_BUCKET"
  s3-region: "$AWS_REGION"
  enable-s3: "true"

  # IPFS Configuration
  ipfs-api: "http://ipfs:5001"
  ipfs-cluster-api: "http://ipfs-cluster:9094"

  # ClamAV Configuration
  clamav-address: "clamav:3310"
  clamav-timeout: "300"
  clamav-max-retries: "3"
  clamav-fallback-mode: "strict"

  # Logging
  log-level: "info"
  log-format: "json"

  # Upload Limits
  max-upload-size: "5368709120"  # 5GB
  chunk-size: "33554432"  # 32MB
  max-concurrent-uploads: "10"

  # Processing
  max-processing-workers: "4"
  processing-timeout: "3600"

  # Rate Limiting
  rate-limit-requests: "100"
  rate-limit-window: "60"

  # CORS
  cors-allowed-origins: "*"
  cors-allowed-methods: "GET,POST,PUT,DELETE,OPTIONS,PATCH"
  cors-allowed-headers: "Accept,Authorization,Content-Type,X-CSRF-Token,X-Requested-With,Idempotency-Key"

  # Encoding
  encoding-scheduler-interval-seconds: "5"
  encoding-scheduler-burst: "3"

  # Feature Flags
  enable-ipfs-cluster: "true"
  enable-encoding-scheduler: "true"
  require-ipfs: "true"
  enable-iota: "false"
  enable-activitypub: "true"
  enable-atproto: "false"
EOF
echo -e "${GREEN}✓ ConfigMap created${NC}"

# Deploy application manifests
echo -e "${YELLOW}Deploying application manifests...${NC}"
K8S_BASE_DIR="../../k8s/base"

# Update deployment to use the correct namespace and service account
kubectl apply -f "$K8S_BASE_DIR/deployment.yaml" -n "$NAMESPACE"
kubectl apply -f "$K8S_BASE_DIR/service.yaml" -n "$NAMESPACE"
kubectl apply -f "$K8S_BASE_DIR/hpa.yaml" -n "$NAMESPACE"
kubectl apply -f "$K8S_BASE_DIR/ingress.yaml" -n "$NAMESPACE"

echo -e "${GREEN}✓ Application manifests deployed${NC}"

# Deploy monitoring
if [ -d "../../k8s/monitoring" ]; then
    echo -e "${YELLOW}Deploying monitoring stack...${NC}"
    kubectl apply -f ../../k8s/monitoring/ -n "$NAMESPACE"
    echo -e "${GREEN}✓ Monitoring deployed${NC}"
fi

echo ""
echo -e "${GREEN}=== Deployment Complete ===${NC}"
echo ""
echo "Cluster: $EKS_CLUSTER_NAME"
echo "Namespace: $NAMESPACE"
echo ""
echo "Next steps:"
echo "1. Check pod status: kubectl get pods -n $NAMESPACE"
echo "2. View logs: kubectl logs -f deployment/vidra-api -n $NAMESPACE"
echo "3. Get ingress: kubectl get ingress -n $NAMESPACE"
echo ""
