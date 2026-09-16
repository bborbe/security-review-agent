---
assignee: build-fixer-agent
build_id: 88027472-8599-454f-8112-569739235dad
commit: ""
dashboard: mdm-duplicate-finder
refs_resolved: "false"
repo: ""
stage: master
status: todo
task_identifier: 9411d991-5921-5606-9a46-d9ec5bd3c0cd
task_type: build-fix
---
# Build Failure: mdm-duplicate-finder

A master build is failing. Analyze the Cloud Build log and fix the root cause.

| Field | Value |
|---|---|
| Dashboard name | `mdm-duplicate-finder` |
| Build ID | `88027472-8599-454f-8112-569739235dad` |
| Repo | `` |
| Commit | `` |
| Dir | `duplicate/finder` |
| Trigger URL | https://console.cloud.google.com/cloud-build/builds;region=europe-west3/88027472-8599-454f-8112-569739235dad?project=159165033271 |

## Log fetch

```bash
gcloud builds log 88027472-8599-454f-8112-569739235dad --project=smedia-brogrammers --region=europe-west3
```
