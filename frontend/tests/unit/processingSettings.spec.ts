import { normalizeProcessingSettings } from '../../src/processingSettings'

describe('图片处理设置', () => {
  it('为旧版设置补齐安全默认值', () => {
    expect(normalizeProcessingSettings()).toEqual({
      generate_webp: false,
      webp_quality: 82,
      strip_metadata: false,
      watermark_enabled: false,
      watermark_text: '',
      watermark_position: 'bottom-right',
    })
  })

  it('保留合法配置并纠正越界质量与未知位置', () => {
    expect(normalizeProcessingSettings({
      generate_webp: true,
      webp_quality: 101,
      strip_metadata: true,
      watermark_enabled: true,
      watermark_text: 'Feather',
      watermark_position: 'unknown' as 'center',
    })).toMatchObject({
      generate_webp: true,
      webp_quality: 82,
      strip_metadata: true,
      watermark_enabled: true,
      watermark_text: 'Feather',
      watermark_position: 'bottom-right',
    })
  })
})
