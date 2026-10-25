#!/bin/bash
#---
# name: Dev
# description: The dev loop, rebuilding watchnbuild whenever its source changes
#---

set -euo pipefail

exec go tool task dev
