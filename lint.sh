#!/bin/bash

# Local linting script for fontlet project
# This runs the same linting checks as the GitHub CI workflow

echo "🔍 Running golangci-lint..."
~/go/bin/golangci-lint run --timeout=5m

if [ $? -eq 0 ]; then
    echo "✅ All linting checks passed!"
else
    echo "❌ Linting failed. Please fix the issues above."
    exit 1
fi