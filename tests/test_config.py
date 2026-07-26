"""
Test configuration for subscriptions-api E2E tests.

Production domains from devops-k8s/apps/subscription-api/values.yaml
"""

import os
from dataclasses import dataclass


@dataclass
class TestConfig:
    """Configuration for subscription service E2E tests."""
    
    # Production API URLs (with /api/v1 path prefix for subscriptions-api)
    API_BASE_URL: str = "https://pricingapi.codevertexafrica.com/api/v1"
    SUBSCRIPTIONS_API_BASE_URL: str = "https://pricingapi.codevertexafrica.com/api/v1"
    AUTH_API_URL: str = "https://sso.codevertexafrica.com"
    
    # Frontend URL
    FRONTEND_URL: str = "https://subscriptions.codevertexafrica.com"
    
    # Test tenant
    TENANT_SLUG: str = "urban-loft"
    
    # Test credentials (from auth-api seed script)
    # Demo user - safe to share, has member role in all tenants
    TEST_EMAIL: str = os.getenv("TEST_EMAIL", "demo@bengobox.dev")
    TEST_PASSWORD: str = os.getenv("TEST_PASSWORD", "DemoUser2024!")
    
    # Staff/Admin credentials for urban-loft tenant
    STAFF_EMAIL: str = os.getenv("STAFF_EMAIL", "staff@urban-loft.com")
    STAFF_PASSWORD: str = os.getenv("STAFF_PASSWORD", "Staffurban-loft2024!")
    
    # Admin credentials for urban-loft tenant  
    ADMIN_EMAIL: str = os.getenv("ADMIN_EMAIL", "admin@theurbanloftcafe.com")
    ADMIN_PASSWORD: str = os.getenv("ADMIN_PASSWORD", "TenantAdmin2024!")
    
    TEST_PHONE: str = os.getenv("TEST_PHONE", "+254700000001")
    
    # Timeouts
    DEFAULT_TIMEOUT: int = 30
    
    # Auth endpoints
    AUTH_TOKEN_URL: str = "https://sso.codevertexafrica.com/api/v1/token"
    AUTH_ME_URL: str = "https://sso.codevertexafrica.com/api/v1/auth/me"


# Default config instance
config = TestConfig()
