import type { ImageItem } from './types'

export type LinkFormat = 'url' | 'markdown' | 'html' | 'bbcode'
export type LinkSeparator = 'newline' | 'blank-line' | 'space'

export interface CopyPreferences {
  format: LinkFormat
  autoCopy: boolean
  separator: LinkSeparator
}

export const COPY_FORMAT_KEY = 'feather-copy-format'
export const AUTO_COPY_KEY = 'feather-auto-copy'
export const COPY_SEPARATOR_KEY = 'feather-copy-separator'

export const linkFormatOptions: { label: string; value: LinkFormat }[] = [
  { label: '原始 URL', value: 'url' },
  { label: 'Markdown', value: 'markdown' },
  { label: 'HTML', value: 'html' },
  { label: 'BBCode', value: 'bbcode' },
]

export const linkSeparatorOptions: { label: string; value: LinkSeparator }[] = [
  { label: '每行一条', value: 'newline' },
  { label: '空行分隔', value: 'blank-line' },
  { label: '空格分隔', value: 'space' },
]

const formats = new Set<LinkFormat>(linkFormatOptions.map((item) => item.value))
const separators = new Set<LinkSeparator>(linkSeparatorOptions.map((item) => item.value))

function escapeHTML(value: string) {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('"', '&quot;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
}

function escapeMarkdown(value: string) {
  return value.replaceAll('\\', '\\\\').replaceAll('[', '\\[').replaceAll(']', '\\]')
}

export function formatImageLink(image: Pick<ImageItem, 'original_name' | 'url'>, format: LinkFormat) {
  let imageURL = image.url
  try {
    imageURL = new URL(image.url, window.location.origin).href
  } catch {
    // 保留无法解析的原始值，由调用方继续展示。
  }
  if (format === 'markdown') return `![${escapeMarkdown(image.original_name)}](${imageURL})`
  if (format === 'html') return `<img src="${escapeHTML(imageURL)}" alt="${escapeHTML(image.original_name)}">`
  if (format === 'bbcode') return `[img]${imageURL}[/img]`
  return imageURL
}

export function joinImageLinks(
  images: Pick<ImageItem, 'original_name' | 'url'>[],
  format: LinkFormat,
  separator: LinkSeparator,
) {
  const joiner = separator === 'blank-line' ? '\n\n' : separator === 'space' ? ' ' : '\n'
  return images.map((image) => formatImageLink(image, format)).join(joiner)
}

export function readCopyPreferences(): CopyPreferences {
  const storedFormat = localStorage.getItem(COPY_FORMAT_KEY) as LinkFormat | null
  const storedSeparator = localStorage.getItem(COPY_SEPARATOR_KEY) as LinkSeparator | null
  return {
    format: storedFormat && formats.has(storedFormat) ? storedFormat : 'markdown',
    autoCopy: localStorage.getItem(AUTO_COPY_KEY) === 'true',
    separator: storedSeparator && separators.has(storedSeparator) ? storedSeparator : 'newline',
  }
}

export function writeCopyPreferences(preferences: CopyPreferences) {
  localStorage.setItem(COPY_FORMAT_KEY, preferences.format)
  localStorage.setItem(AUTO_COPY_KEY, String(preferences.autoCopy))
  localStorage.setItem(COPY_SEPARATOR_KEY, preferences.separator)
}
