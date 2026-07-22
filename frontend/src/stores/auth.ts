import { defineStore } from 'pinia'
import { api, postJSON, setCSRF } from '../api'
import type { UploadBootstrap } from '../types'

interface Status {
  initialized: boolean
  authenticated: boolean
  csrf_token?: string
  upload?: UploadBootstrap
}

interface AuthState {
  initialized: boolean
  authenticated: boolean
  checking: boolean
  uploadBootstrap: UploadBootstrap | null
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({ initialized: false, authenticated: false, checking: true, uploadBootstrap: null }),
  actions: {
    async check() {
      this.checking = true
      try {
        const state = await api<Status>('/auth/status')
        this.initialized = state.initialized
        this.authenticated = state.authenticated
        this.uploadBootstrap = state.upload || null
        setCSRF(state.csrf_token || '')
      } finally { this.checking = false }
    },
    takeUploadBootstrap() {
      const bootstrap = this.uploadBootstrap
      this.uploadBootstrap = null
      return bootstrap
    },
    async login(username: string, password: string) {
      const result = await postJSON<{ csrf_token: string }>('/auth/login', { username, password })
      setCSRF(result.csrf_token)
      this.authenticated = true
      this.initialized = true
      this.uploadBootstrap = null
    },
    async initialize(username: string, password: string, site_url: string) {
      const result = await postJSON<{ csrf_token: string }>('/auth/initialize', { username, password, site_url })
      setCSRF(result.csrf_token)
      this.authenticated = true
      this.initialized = true
      this.uploadBootstrap = null
    },
    async logout() {
      await postJSON('/auth/logout')
      setCSRF('')
      this.authenticated = false
      this.uploadBootstrap = null
    },
    reset() { this.authenticated = false; this.uploadBootstrap = null; setCSRF('') },
  },
})
