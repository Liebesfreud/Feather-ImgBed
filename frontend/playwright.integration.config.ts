import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/integration',
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:4174',
    trace: 'on-first-retry',
  },
  projects: [
    { name: 'chromium-real-backend', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: `bash -lc 'data_dir="$(mktemp -d)"; trap "rm -rf \\"$data_dir\\"" EXIT; cd ..; go run . serve -listen 127.0.0.1:4174 -data "$data_dir"'`,
    url: 'http://127.0.0.1:4174/healthz',
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
