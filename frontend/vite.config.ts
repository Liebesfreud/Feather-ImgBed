import { brotliCompressSync, constants, gzipSync } from 'node:zlib'
import { readFile, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { defineConfig, loadEnv, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

function precompressAssets(): Plugin {
  const compressible = /\.(?:css|html|js|json|svg|txt|webmanifest|xml)$/i
  return {
    name: 'feather-precompress-assets',
    apply: 'build',
    async writeBundle(options, bundle) {
      if (!options.dir) return
      const fileNames = new Set([...Object.keys(bundle), 'index.html'])
      await Promise.all(Array.from(fileNames).filter((fileName) => compressible.test(fileName)).map(async (fileName) => {
        const filename = resolve(options.dir!, fileName)
        const buffer = await readFile(filename)
        if (buffer.length < 256) return

        const gzip = gzipSync(buffer, { level: 9 })
        const brotli = brotliCompressSync(buffer, {
          params: { [constants.BROTLI_PARAM_QUALITY]: 9 },
        })
        await Promise.all([
          gzip.length < buffer.length ? writeFile(`${filename}.gz`, gzip) : Promise.resolve(),
          brotli.length < buffer.length ? writeFile(`${filename}.br`, brotli) : Promise.resolve(),
        ])
      }))
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const apiTarget = env.FEATHER_API_PROXY || 'http://127.0.0.1:8080'
  return {
    plugins: [vue(), precompressAssets()],
    build: {
      outDir: '../internal/app/web/dist',
      emptyOutDir: true,
      sourcemap: false,
    },
    server: {
      port: 5173,
      proxy: {
        '/api': apiTarget,
        '/files': apiTarget,
        '/healthz': apiTarget,
      },
    },
  }
})
