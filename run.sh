#!/bin/bash
# Load environment variables from .env and run ponos
export $(cat .env | grep -v '^#' | xargs)
go run cmd/*.go "$@"