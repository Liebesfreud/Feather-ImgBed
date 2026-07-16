import { defineStore } from 'pinia'
import { api, postJSON, setCSRF } from '../api'

interface Status { initialized: boolean; authenticated: boolean; csrf_token?: string }

export const useAuthStore = defineStore('auth', {
  state: () => ({ initialized: false, authenticated: false, checking: true }),
  actions: {
    async check() {
      this.checking = true
      try {
        const state = await api<Status>('/auth/status')
        this.initialized = state.initialized
        this.authenticated = state.authenticated
        if (state.csrf_token) setCSRF(state.csrf_token)
      } finally { this.checking = false }
    },
    async login(username: string, password: string) {
      const result = await postJSON<{ csrf_token: string }>('/auth/login', { username, password })
      setCSRF(result.csrf_token)
      this.authenticated = true
      this.initialized = true
    },
    async initialize(username: string, password: string, site_url: string) {
      const result = await postJSON<{ csrf_token: string }>('/auth/initialize', { username, password, site_url })
      setCSRF(result.csrf_token)
      this.authenticated = true
      this.initialized = true
    },
    async logout() {
      await postJSON('/auth/logout')
      setCSRF('')
      this.authenticated = false
    },
    reset() { this.authenticated = false; setCSRF('') },
  },
})
