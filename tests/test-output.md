# Subscription Service E2E Test Results

**Test Date:** 2026-03-09 21:51:45
**Tenant:** urban-loft
**Environment:** Production APIs

## Summary

**Total: 6/9 passed (66% success rate)**

- Passed: 6
- Failed: 3
- Skipped: 0

## Test Details

### AUTH - sso_health

- **Status:** ✅ PASS
- **Details:** SSO service is healthy
- **Timestamp:** 2026-03-09T21:51:23.051620

---

### AUTH - sso_oidc

- **Status:** ✅ PASS
- **Details:** OIDC discovery successful
- **Timestamp:** 2026-03-09T21:51:24.148231
- **Response Data:**
```json
{
  "token_endpoint": "https://sso.codevertexafrica.com/api/v1/token",
  "issuer": "https://sso.codevertexafrica.com"
}
```

---

### AUTH - sso_login

- **Status:** ✅ PASS
- **Details:** Login successful
- **Timestamp:** 2026-03-09T21:51:27.054724
- **Response Data:**
```json
{
  "token_preview": "eyJhbGciOiJSUzI1NiIsImtpZCI6ImRkOTZlMGI1YjdlMWQxMz..."
}
```

---

### AUTH - sso_me

- **Status:** ✅ PASS
- **Details:** User 46898d72-650b-4f2f-8ccc-10a18aae4df6 authenticated
- **Timestamp:** 2026-03-09T21:51:28.719232
- **Response Data:**
```json
{
  "user_id": "46898d72-650b-4f2f-8ccc-10a18aae4df6",
  "email": "demo@bengobox.dev",
  "roles": [
    "member"
  ],
  "tenant": null
}
```

---

### SUB - sub_health

- **Status:** ❌ FAIL
- **Details:** HTTP 404
- **Timestamp:** 2026-03-09T21:51:31.069442
- **Response Data:**
```json
{
  "status_code": 404,
  "response": "<!DOCTYPE html>\n<html style=\"height:100%\">\n<head>\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, shrink-to-fit=no\" />\n<title> 404 Not Found\r\n</title><style>@media (prefers-color-scheme:dark){body{background-color:#000!important}}</style></head>\n<body style=\"color: #444; margin:0;font: normal 14px/20px Arial, Helvetica, sans-serif; height:100%; background-color: #fff;\">\n<div style=\"height:auto; min-height:100%; \">     <div style=\"text-align: center; width:800px; margin-left: "
}
```

---

### SUB - fetch_plans

- **Status:** ❌ FAIL
- **Details:** HTTP 404
- **Timestamp:** 2026-03-09T21:51:40.375734
- **Response Data:**
```json
{
  "status_code": 404,
  "response": "<!DOCTYPE html>\n<html style=\"height:100%\">\n<head>\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, shrink-to-fit=no\" />\n<title> 404 Not Found\r\n</title><style>@media (prefers-color-s"
}
```

---

### SUB - fetch_subscriptions

- **Status:** ✅ PASS
- **Details:** Endpoint status: 404
- **Timestamp:** 2026-03-09T21:51:43.431608
- **Response Data:**
```json
{
  "status_code": 404
}
```

---

### SUB - create_subscription

- **Status:** ❌ FAIL
- **Details:** No plans available
- **Timestamp:** 2026-03-09T21:51:43.433372

---

### SUB - check_features

- **Status:** ✅ PASS
- **Details:** Feature endpoint: 404
- **Timestamp:** 2026-03-09T21:51:45.940917
- **Response Data:**
```json
{
  "status_code": 404
}
```

---

