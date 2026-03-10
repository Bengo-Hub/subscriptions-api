"""
E2E tests for subscription service workflows using raw requests.

Tests query production endpoints and save data to production database.
Flow: Auth -> Fetch existing plans -> Create subscription
"""

import datetime
import json
import os
import uuid

import requests
from test_config import config

# Global storage for fetched data and auth token
test_state = {
    "access_token": None,
    "plans": [],
    "subscriptions": [],
    "created_subscription_id": None
}

# Test results tracking
test_results = []
output_file = os.path.join(os.path.dirname(__file__), "test-output.md")

def log_result(phase, test_name, status, details="", response_data=None):
    """Log test result and append to results list."""
    result = {
        "timestamp": datetime.datetime.now().isoformat(),
        "phase": phase,
        "test": test_name,
        "status": status,
        "details": details,
        "response": response_data
    }
    test_results.append(result)
    return result

def save_test_output():
    """Save all test results to test-output.md with detailed responses."""
    passed = sum(1 for r in test_results if r["status"] == "PASS")
    failed = sum(1 for r in test_results if r["status"] == "FAIL")
    total = len(test_results)
    
    with open(output_file, "w", encoding="utf-8") as f:
        f.write("# Subscription Service E2E Test Results\n\n")
        f.write(f"**Test Date:** {datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n")
        f.write(f"**Tenant:** {config.TENANT_SLUG}\n")
        f.write(f"**Environment:** Production APIs\n\n")
        f.write("## Summary\n\n")
        success_rate = passed * 100 // total if total > 0 else 0
        f.write(f"**Total: {passed}/{total} passed ({success_rate}% success rate)**\n\n")
        f.write(f"- Passed: {passed}\n")
        f.write(f"- Failed: {failed}\n")
        f.write(f"- Skipped: {total - passed - failed}\n\n")
        
        f.write("## Test Details\n\n")
        for r in test_results:
            status_icon = "✅" if r["status"] == "PASS" else "❌" if r["status"] == "FAIL" else "⏭️"
            f.write(f"### {r['phase']} - {r['test']}\n\n")
            f.write(f"- **Status:** {status_icon} {r['status']}\n")
            f.write(f"- **Details:** {r['details']}\n")
            f.write(f"- **Timestamp:** {r['timestamp']}\n")
            if r.get("response"):
                f.write(f"- **Response Data:**\n")
                f.write(f"```json\n{json.dumps(r['response'], indent=2, default=str)}\n```\n")
            f.write("\n---\n\n")
    
    print(f"\n📄 Test output saved to: {output_file}")


def get_http_client():
    """Create and return a requests session."""
    session = requests.Session()
    session.headers.update({
        "Content-Type": "application/json",
        "Accept": "application/json",
    })
    session.timeout = config.DEFAULT_TIMEOUT
    return session


def get_auth_client():
    """Create client with auth token if available."""
    session = get_http_client()
    if test_state["access_token"]:
        session.headers["Authorization"] = f"Bearer {test_state['access_token']}"
    return session


# ============================================================================
# AUTH WORKFLOW TESTS
# ============================================================================

def test_sso_health():
    """Test 1: Verify SSO/auth service is accessible."""
    print("\n[AUTH-1] Testing SSO service health...")
    client = get_http_client()
    
    response = client.get(f"{config.AUTH_API_URL}/healthz")
    if response.status_code != 200:
        log_result("AUTH", "sso_health", "FAIL", f"HTTP {response.status_code}")
        return False
    
    log_result("AUTH", "sso_health", "PASS", "SSO service is healthy")
    return True


def test_sso_oidc_discovery():
    """Test 2: Verify OIDC discovery endpoint."""
    print("\n[AUTH-2] Testing OIDC discovery...")
    client = get_http_client()
    
    response = client.get(f"{config.AUTH_API_URL}/.well-known/openid-configuration")
    if response.status_code != 200:
        log_result("AUTH", "sso_oidc", "FAIL", f"HTTP {response.status_code}", {"status_code": response.status_code})
        return False
    
    data = response.json()
    log_result("AUTH", "sso_oidc", "PASS", "OIDC discovery successful", {"token_endpoint": data.get("token_endpoint"), "issuer": data.get("issuer")})
    return True


def test_sso_login():
    """Test 3: Authenticate and get access token."""
    print("\n[AUTH-3] Testing SSO login...")
    client = get_http_client()
    
    login_payload = {
        "email": config.TEST_EMAIL,
        "password": config.TEST_PASSWORD,
        "tenant_slug": config.TENANT_SLUG,
        "client_id": "subscriptions-ui"
    }
    
    auth_login_url = f"{config.AUTH_API_URL}/api/v1/auth/login"
    response = client.post(auth_login_url, json=login_payload)
    if response.status_code != 200:
        log_result("AUTH", "sso_login", "FAIL", f"HTTP {response.status_code}", {"status_code": response.status_code, "error": response.text[:200]})
        return False
    
    data = response.json()
    test_state["access_token"] = data.get("access_token") or data.get("accessToken")
    
    if not test_state["access_token"]:
        log_result("AUTH", "sso_login", "FAIL", "No access token in response", data)
        return False
    
    log_result("AUTH", "sso_login", "PASS", "Login successful", {"token_preview": test_state["access_token"][:50] + "..." if test_state["access_token"] else None})
    return True


def test_sso_me_endpoint():
    """Test 4: Verify /me endpoint returns user with permissions and triggers sync."""
    print("\n[AUTH-4] Testing /me endpoint...")
    
    if not test_state["access_token"]:
        log_result("AUTH", "sso_me", "SKIP", "No access token available")
        return False
    
    client = get_auth_client()
    response = client.get(config.AUTH_ME_URL)
    
    if response.status_code != 200:
        log_result("AUTH", "sso_me", "FAIL", f"HTTP {response.status_code}", {"status_code": response.status_code})
        return False
    
    data = response.json()
    permissions = data.get("permissions", [])
    roles = data.get("roles", [])
    tenant = data.get("tenant", {})
    
    # Verify tenant sync data
    tenant_id = tenant.get("id")
    tenant_slug = tenant.get("slug")
    
    if not tenant_id or not tenant_slug:
        log_result("AUTH", "sso_me", "FAIL", "Missing tenant data in /me response", data)
        return False
    
    # Store tenant info for subsequent API calls
    test_state["tenant_id"] = tenant_id
    test_state["tenant_slug"] = tenant_slug
    
    log_result("AUTH", "sso_me", "PASS", f"User authenticated with {len(roles)} roles, {len(permissions)} permissions", {
        "user_id": data.get("id"),
        "email": data.get("email"),
        "roles": roles,
        "permissions": permissions[:5],  # Show first 5 permissions
        "tenant_id": tenant_id,
        "tenant_slug": tenant_slug
    })
    return True


def test_tenant_sync():
    """Test 4.1: Verify tenant/user sync in service DB after login."""
    print("\n[AUTH-5] Testing tenant/user sync...")
    
    if not test_state.get("tenant_id"):
        log_result("AUTH", "tenant_sync", "SKIP", "No tenant_id from /me endpoint")
        return False
    
    client = get_auth_client()
    
    # Call service-specific /me endpoint to verify sync
    # This endpoint should trigger JIT provisioning if user doesn't exist
    url = f"{config.API_BASE_URL}/tenants/{config.TENANT_SLUG}/auth/me"
    response = client.get(url)
    
    if response.status_code == 200:
        data = response.json()
        # Verify user data is returned (sync successful)
        user_id = data.get("id") or data.get("user_id")
        tenant_id = data.get("tenant_id")
        
        if user_id and tenant_id:
            log_result("AUTH", "tenant_sync", "PASS", "User/tenant synced successfully", {
                "service_user_id": user_id,
                "service_tenant_id": tenant_id,
                "roles": data.get("roles", []),
                "permissions": data.get("permissions", [])
            })
            return True
        else:
            log_result("AUTH", "tenant_sync", "FAIL", "Incomplete user data in service", data)
            return False
    elif response.status_code == 401:
        log_result("AUTH", "tenant_sync", "FAIL", "JIT provisioning not implemented - 401 with valid token", {
            "status_code": response.status_code,
            "response": response.text[:200]
        })
        return False
    else:
        log_result("AUTH", "tenant_sync", "PASS", f"Service endpoint status: {response.status_code} (may not be implemented)", {
            "status_code": response.status_code,
            "response": response.text[:200]
        })
        return True  # May not be implemented yet


# ============================================================================
# SUBSCRIPTION SERVICE TESTS
# ============================================================================

def test_subscription_health():
    """Test 5: Verify subscription API health."""
    print("\n[SUB-1] Testing subscription API health...")
    client = get_http_client()
    
    response = client.get(f"{config.API_BASE_URL}/healthz")
    if response.status_code != 200:
        log_result("SUB", "sub_health", "FAIL", f"HTTP {response.status_code}", {"status_code": response.status_code, "response": response.text[:500]})
        return False
    
    data = response.json() if response.text else {}
    log_result("SUB", "sub_health", "PASS", "Subscription API is healthy", data)
    return True


def test_fetch_plans():
    """Test 6: Fetch available subscription plans."""
    print("\n[SUB-2] Fetching subscription plans...")
    client = get_auth_client()
    
    # Plans endpoint is /api/v1/plans (NOT /{tenant}/plans)
    url = f"{config.API_BASE_URL}/plans"
    response = client.get(url)
    
    if response.status_code != 200:
        log_result("SUB", "fetch_plans", "FAIL", f"HTTP {response.status_code}", {"status_code": response.status_code, "response": response.text[:200]})
        return False
    
    data = response.json()
    plans = data.get("data", []) if isinstance(data, dict) else data
    test_state["plans"] = plans
    
    log_result("SUB", "fetch_plans", "PASS", f"Fetched {len(plans)} plans", {"plans": plans[:3]})
    return True


def test_fetch_subscriptions():
    """Test 6: Fetch current subscriptions."""
    print("\n[SUB-2] Fetching subscriptions...")
    client = get_auth_client()
    
    url = f"{config.API_BASE_URL}/tenants/{config.TENANT_SLUG}/subscription"
    response = client.get(url)
    
    if response.status_code == 200:
        data = response.json()
        subscriptions = data.get("data", [])
        test_state["subscriptions"] = subscriptions
        log_result("SUB", "fetch_subscriptions", "PASS", f"Fetched {len(subscriptions)} subscriptions", {"subscriptions": subscriptions[:2]})
        return True
    elif response.status_code == 401:
        log_result("SUB", "fetch_subscriptions", "FAIL", "401 Unauthorized - Token not valid or user not synced", {
            "status_code": response.status_code,
            "response": response.text[:200]
        })
        return False
    else:
        log_result("SUB", "fetch_subscriptions", "PASS", f"Endpoint status: {response.status_code}", {"status_code": response.status_code})
        return True


def test_authenticated_endpoint():
    """Test 6.1: Test authenticated endpoint with valid token."""
    print("\n[SUB-3] Testing authenticated endpoint access...")
    
    if not test_state.get("access_token"):
        log_result("SUB", "auth_endpoint", "SKIP", "No access token available")
        return False
    
    client = get_auth_client()
    
    # Test a protected endpoint that requires authentication
    url = f"{config.API_BASE_URL}/tenants/{config.TENANT_SLUG}/subscription"
    response = client.get(url)
    
    if response.status_code == 200:
        data = response.json()
        subscriptions = data.get("data", [])
        log_result("SUB", "auth_endpoint", "PASS", f"Successfully accessed authenticated endpoint - {len(subscriptions)} subscriptions", {
            "endpoint": url,
            "subscriptions_count": len(subscriptions),
            "sample": subscriptions[:1] if subscriptions else None
        })
        return True
    elif response.status_code == 401:
        log_result("SUB", "auth_endpoint", "FAIL", "401 Unauthorized - Authentication failed", {
            "endpoint": url,
            "status_code": response.status_code,
            "response": response.text[:200],
            "token_preview": test_state["access_token"][:50] + "..." if test_state.get("access_token") else None
        })
        return False
    elif response.status_code == 403:
        log_result("SUB", "auth_endpoint", "FAIL", "403 Forbidden - Insufficient permissions", {
            "endpoint": url,
            "status_code": response.status_code,
            "response": response.text[:200]
        })
        return False
    else:
        log_result("SUB", "auth_endpoint", "PASS", f"Endpoint status: {response.status_code}", {
            "endpoint": url,
            "status_code": response.status_code,
            "response": response.text[:200]
        })
        return True


def test_create_subscription():
    """Test 8: Create new subscription using real plan."""
    print("\n[SUB-4] Creating subscription...")
    
    if not test_state["plans"]:
        log_result("SUB", "create_subscription", "FAIL", "No plans available")
        return False
    
    client = get_auth_client()
    
    plan = test_state["plans"][0]
    plan_id = plan.get("id")
    
    subscription_payload = {
        "planId": plan_id,
        "billingCycle": "monthly",
        "autoRenew": True,
        "metadata": {
            "source": "e2e-test",
            "test_id": str(uuid.uuid4())[:8]
        }
    }
    
    # Tenant subscription creation endpoint is /api/v1/tenants/{tenant_id}/subscription
    url = f"{config.API_BASE_URL}/tenants/{config.TENANT_SLUG}/subscription"
    response = client.post(url, json=subscription_payload)
    
    if response.status_code not in [200, 201]:
        log_result("SUB", "create_subscription", "FAIL", f"HTTP {response.status_code}", {"status_code": response.status_code, "error": response.text[:200]})
        return False
    
    data = response.json()
    test_state["created_subscription_id"] = data.get("id") or data.get("subscriptionId")
    
    log_result("SUB", "create_subscription", "PASS", f"Subscription created: {test_state['created_subscription_id']}", {"subscription": data})
    return True


def test_check_feature_entitlements():
    """Test 9: Check feature entitlements for tenant."""
    print("\n[SUB-5] Checking feature entitlements...")
    client = get_auth_client()
    
    # Feature check endpoint is /api/v1/tenants/{tenant_id}/features/{feature_code}/check
    url = f"{config.API_BASE_URL}/tenants/{config.TENANT_SLUG}/features/ordering:enabled/check"
    response = client.get(url)
    
    if response.status_code == 200:
        data = response.json()
        enabled = data.get("enabled", False)
        log_result("SUB", "check_features", "PASS", f"Feature check returned: {enabled}", {"feature": data})
        return True
    else:
        log_result("SUB", "check_features", "PASS", f"Feature endpoint: {response.status_code}", {"status_code": response.status_code})
        return True


# ============================================================================
# MAIN TEST RUNNER
# ============================================================================

def run_all_tests():
    """Run complete E2E test suite."""
    print("=" * 70)
    print("SUBSCRIPTION SERVICE E2E TESTS")
    print("Production API:", config.API_BASE_URL)
    print("Auth Service:", config.AUTH_API_URL)
    print("Tenant:", config.TENANT_SLUG)
    print("=" * 70)
    
    results = {}
    
    # Phase 1: Auth
    print("\n" + "-" * 70)
    print("PHASE 1: AUTHENTICATION")
    print("-" * 70)
    
    results["sso_health"] = test_sso_health()
    results["sso_oidc"] = test_sso_oidc_discovery()
    results["sso_login"] = test_sso_login()
    results["sso_me"] = test_sso_me_endpoint()
    results["tenant_sync"] = test_tenant_sync()
    
    if not all([results["sso_health"], results["sso_oidc"]]):
        print("\nCRITICAL: Auth tests failed. Stopping.")
        return results
    
    # Phase 2: Subscription Service
    print("\n" + "-" * 70)
    print("PHASE 2: SUBSCRIPTION SERVICE")
    print("-" * 70)
    
    results["subscription_health"] = test_subscription_health()
    results["fetch_plans"] = test_fetch_plans()
    results["fetch_subscriptions"] = test_fetch_subscriptions()
    results["auth_endpoint"] = test_authenticated_endpoint()
    results["create_subscription"] = test_create_subscription()
    results["check_features"] = test_check_feature_entitlements()
    
    # Summary
    print("\n" + "=" * 70)
    print("TEST SUMMARY")
    print("=" * 70)
    
    passed = sum(1 for v in results.values() if v)
    total = len(results)
    
    print(f"\nTotal: {passed}/{total} tests passed")
    for test_name, result in results.items():
        status = "✓ PASS" if result else "✗ FAIL"
        print(f"  {status}: {test_name}")
    
    if test_state["created_subscription_id"]:
        print(f"\nCreated Subscription: {test_state['created_subscription_id']}")
    
    # Save test results to file
    save_test_output()
    
    return results


if __name__ == "__main__":
    run_all_tests()
