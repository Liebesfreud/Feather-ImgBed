import { ApiError, setCSRF, uploadFile } from '../../src/api'

class FakeXMLHttpRequest {
  static latest: FakeXMLHttpRequest
  upload: { onprogress?: (event: ProgressEvent) => void } = {}
  withCredentials = false
  status = 0
  responseText = ''
  onload?: () => void
  onerror?: () => void
  onabort?: () => void
  open = vi.fn()
  setRequestHeader = vi.fn()
  send = vi.fn()

  constructor() {
    FakeXMLHttpRequest.latest = this
  }

  abort() {
    this.onabort?.()
  }
}

describe('XHR 上传辅助', () => {
  beforeEach(() => {
    vi.stubGlobal('XMLHttpRequest', FakeXMLHttpRequest)
    setCSRF('csrf-token')
  })

  afterEach(() => {
    setCSRF('')
    vi.unstubAllGlobals()
  })

  it('携带同源凭据、CSRF 并报告真实进度', async () => {
    const progress = vi.fn()
    const request = uploadFile<{ id: string }>('/images', new FormData(), progress)
    const xhr = FakeXMLHttpRequest.latest

    expect(xhr.open).toHaveBeenCalledWith('POST', '/api/v1/images')
    expect(xhr.withCredentials).toBe(true)
    expect(xhr.setRequestHeader).toHaveBeenCalledWith('X-CSRF-Token', 'csrf-token')

    xhr.upload.onprogress?.({ lengthComputable: true, loaded: 25, total: 100 } as ProgressEvent)
    expect(progress).toHaveBeenCalledWith(25)

    xhr.status = 201
    xhr.responseText = JSON.stringify({ success: true, data: { id: 'image-1' }, request_id: 'test' })
    xhr.onload?.()
    await expect(request.promise).resolves.toEqual({ id: 'image-1' })
  })

  it('取消时返回可识别的错误码', async () => {
    const request = uploadFile('/images', new FormData(), vi.fn())
    request.cancel()

    await expect(request.promise).rejects.toMatchObject<ApiError>({ code: 'UPLOAD_ABORTED' })
  })
})
