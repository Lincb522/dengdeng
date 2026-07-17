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
		 loginAgreement.value = s.login_agreement || loginAgreement.value
      document.title = s.site_name
		 return s
    } catch {
      /* keep defaults */
		 return null
    }
  }

  async function login(email: string, password: string, termsRevision = '') {
    const resp = await api.post<LoginResp>('/api/auth/login', { email, password, terms_revision: termsRevision })
    setToken(resp.token)
    user.value = resp.user
  }

  async function register(email: string, password: string, code: string, termsRevision = '') {
    const resp = await api.post<LoginResp>('/api/auth/register', { email, password, code, terms_revision: termsRevision })
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

  return { user, siteName, siteSubtitle, allowRegister, loginAgreement, loadPublicSettings, login, register, fetchMe, logout }
})
