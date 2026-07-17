import type { ProcessingSettings } from './types'

export const defaultProcessingSettings: ProcessingSettings = {
  generate_webp: false,
  webp_quality: 82,
  watermark_enabled: false,
  watermark_text: '',
  watermark_position: 'bottom-right',
}

export function normalizeProcessingSettings(value?: Partial<ProcessingSettings>): ProcessingSettings {
  const quality = Number(value?.webp_quality)
  const positions = new Set<ProcessingSettings['watermark_position']>(['top-left', 'top-right', 'bottom-left', 'bottom-right', 'center'])
  return {
    generate_webp: Boolean(value?.generate_webp),
    webp_quality: Number.isFinite(quality) && quality >= 1 && quality <= 100 ? quality : 82,
    watermark_enabled: Boolean(value?.watermark_enabled),
    watermark_text: typeof value?.watermark_text === 'string' ? value.watermark_text : '',
    watermark_position: value?.watermark_position && positions.has(value.watermark_position) ? value.watermark_position : 'bottom-right',
  }
}
