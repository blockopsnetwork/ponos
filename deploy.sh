#!/usr/bin/env bash

set -euo pipefail

# set env variables
export K8S_DEPLOYMENT_API="ponos"
export NAMESPACE="${ENVIRONMENT}"

echo "##### Starting blockops block proxy deployment..."
sed -i "s|##IMAGE_URL##|${IMG_NAME}|" k8s/$ENVIRONMENT/*.yml

for directory in ingresses services deployments; do
	# check if dir and yml files exists
	if [ $(ls -1 k8s/$ENVIRONMENT/*.yml 2>/dev/null | wc -l) != 0 ]; then
		kubectl --kubeconfig="/home/runner/.kube/config" apply -f k8s/$ENVIRONMENT/
	fi
done

kubectl --kubeconfig="/home/runner/.kube/config" -n ${ENVIRONMENT} rollout status deployment ${K8S_DEPLOYMENT_API} -w --timeout=2m