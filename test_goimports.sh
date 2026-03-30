#!/bin/bash
BADIMPORTS=$(~/go/bin/goimports -l $(go list -f '{{.Dir}}' ./...))
echo "Output: $BADIMPORTS"
