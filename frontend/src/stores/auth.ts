import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, setToken, clearToken, getToken } from '../api/client'
import type { LoginAgreement, PublicSettings, User } from '../api/types'

interface LoginResp {
  token: string
  user: User
}

export const useAuth = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const siteName = ref('DengDeng AI · 蹬蹬ai')
  const siteSubtitle = ref('统一管理模型接入与用量')
  const allowRegister = ref(true)
  const keyMultiGroupEnabled = ref(true)
  const registrationVerification = ref(true)
  const siteCustomization = ref<NonNullable<PublicSettings['site_customization']>>({
		logo_url: '', contact_info: '', docs_url: '', home_content: '', backend_mode_enabled: false, hide_ccs_import_button: false,
    table_default_page_size: 20, table_page_size_options: [10, 20, 50, 100], custom_menu_items: [], custom_endpoints: [],
  })
  const features = ref<NonNullable<PublicSettings['features']>>({
    model_plaza_enabled: true, referral_enabled: true, allow_user_view_error_requests: true,
  })
  const security = ref<NonNullable<PublicSettings['security']>>({
    password_reset_enabled: true, totp_enabled: true, turnstile_enabled: false, turnstile_site_key: '',
  })
	const oauthProviders = ref<Array<{ id: string; name: string }>>([])
  const loginAgreement = ref<LoginAgreement>({
    enabled: false,
    mode: 'modal',
    updated_at: '',
    revision: '',
    documents: [],
  })

  async function loadPublicSettings(): Promise<PublicSettings | null> {
    try {
      const s = await api.get<PublicSettings>('/api/settings')
      siteName.value = s.site_name
		 siteSubtitle.value = s.site_subtitle || siteSubtitle.value
      allowRegister.value = s.allow_register
		 keyMultiGroupEnabled.value = s.key_multi_group_enabled !== false
		 registrationVerification.value = s.registration_verification !== false
		 loginAgreement.value = s.login_agreement || loginAgreement.value
		 const customization = s.site_customization || {}
		 siteCustomization.value = {
			...siteCustomization.value,
			...customization,
			table_page_size_options: customization.table_page_size_options || siteCustomization.value.table_page_size_options || [10, 20, 50, 100],
			custom_menu_items: customization.custom_menu_items || [],
			custom_endpoints: customization.custom_endpoints || [],
		}
		 features.value = { ...features.value, ...(s.features || {}) }
		 security.value = { ...security.value, ...(s.security || {}) }
		 oauthProviders.value = s.oauth_providers || []
      document.title = s.site_name
		 return s
    } catch {
      /* keep defaults */
		 return null
    }
  }

  async function login(email: string, password: string, termsRevision = '', totpCode = '', turnstileToken = '') {
		const resp = await api.post<LoginResp>('/api/auth/login', { email, password, terms_revision: termsRevision, totp_code: totpCode, turnstile_token: turnstileToken })
    setToken(resp.token)
    user.value = resp.user
  }

  async function register(email: string, password: string, code: string, termsRevision = '', referralCode = '', turnstileToken = '') {
		const resp = await api.post<LoginResp>('/api/auth/register', { email, password, code, terms_revision: termsRevision, referral_code: referralCode, turnstile_token: turnstileToken })
    setToken(resp.token)
    user.value = resp.user
  }

  async function fetchMe(): Promise<boolean> {
    if (!getToken()) return false
    try {
      user.value = await api.get<User>('/api/user/me')
      return true
    } catch {
      return false
    }
  }

  function logout() {
    clearToken()
    user.value = null
    window.location.href = '/login'
  }

	return { user, siteName, siteSubtitle, allowRegister, keyMultiGroupEnabled, registrationVerification, siteCustomization, features, security, oauthProviders, loginAgreement, loadPublicSettings, login, register, fetchMe, logout }
})
