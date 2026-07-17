import {
  AUTO_COPY_KEY,
  COPY_FORMAT_KEY,
  COPY_SEPARATOR_KEY,
  formatImageLink,
  joinImageLinks,
  readCopyPreferences,
  writeCopyPreferences,
} from '../../src/linkFormats'

const image = { original_name: '猫咪 [1].jpg', url: 'https://img.example.com/cat.jpg' }

describe('链接格式', () => {
  it.each([
    ['url', 'https://img.example.com/cat.jpg'],
    ['markdown', '![猫咪 \\[1\\].jpg](https://img.example.com/cat.jpg)'],
    ['html', '<img src="https://img.example.com/cat.jpg" alt="猫咪 [1].jpg">'],
    ['bbcode', '[img]https://img.example.com/cat.jpg[/img]'],
  ] as const)('生成 %s 链接', (format, expected) => {
    expect(formatImageLink(image, format)).toBe(expected)
  })

  it('按偏好连接多条链接', () => {
    expect(joinImageLinks([image, image], 'url', 'blank-line')).toBe(`${image.url}\n\n${image.url}`)
  })

  it('复制相对代理路径时补全当前图床地址', () => {
    expect(formatImageLink({ original_name: 'cat.jpg', url: '/s3-files/r2/cat.jpg' }, 'url'))
      .toBe(`${window.location.origin}/s3-files/r2/cat.jpg`)
  })

  it('读写本地复制偏好并忽略非法值', () => {
    writeCopyPreferences({ format: 'bbcode', autoCopy: true, separator: 'space' })
    expect(readCopyPreferences()).toEqual({ format: 'bbcode', autoCopy: true, separator: 'space' })

    localStorage.setItem(COPY_FORMAT_KEY, 'invalid')
    localStorage.setItem(AUTO_COPY_KEY, 'false')
    localStorage.setItem(COPY_SEPARATOR_KEY, 'invalid')
    expect(readCopyPreferences()).toEqual({ format: 'markdown', autoCopy: false, separator: 'newline' })
  })
})
