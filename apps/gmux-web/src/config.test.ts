import { afterEach, describe, expect, it, vi } from 'vitest'
import { saveVSCodeServerConfig } from './config'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('saveVSCodeServerConfig', () => {
  it('patches only the two VS Code settings and returns canonical values', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      ok: true,
      data: {
        settings: {
          fontSize: 16,
          vsCodeServerUrl: 'https://code.example.test',
          vsCodeServerHomeDir: '/home/rhee',
        },
      },
    }), { status: 200, headers: { 'content-type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(saveVSCodeServerConfig({
      vsCodeServerUrl: ' https://code.example.test ',
      vsCodeServerHomeDir: ' /home/rhee/ ',
    })).resolves.toEqual({
      vsCodeServerUrl: 'https://code.example.test',
      vsCodeServerHomeDir: '/home/rhee',
    })

    expect(fetchMock).toHaveBeenCalledOnce()
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/v1/frontend-config')
    expect(init).toMatchObject({ method: 'PATCH', headers: { 'content-type': 'application/json' } })
    expect(JSON.parse(String(init?.body))).toEqual({
      vsCodeServerUrl: 'https://code.example.test',
      vsCodeServerHomeDir: '/home/rhee/',
    })
  })

  it('omits settings that the caller did not change', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      ok: true,
      data: { settings: { vsCodeServerUrl: 'https://new.example.test', vsCodeServerHomeDir: '/preserved/home' } },
    }), { status: 200, headers: { 'content-type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await saveVSCodeServerConfig({ vsCodeServerUrl: 'https://new.example.test' })
    const [, init] = fetchMock.mock.calls[0]
    expect(JSON.parse(String(init?.body))).toEqual({ vsCodeServerUrl: 'https://new.example.test' })
  })

  it('surfaces the daemon error message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      error: { message: 'settings.jsonc could not be safely updated' },
    }), { status: 409, headers: { 'content-type': 'application/json' } })))

    await expect(saveVSCodeServerConfig({ vsCodeServerUrl: '', vsCodeServerHomeDir: '' }))
      .rejects.toThrow('settings.jsonc could not be safely updated')
  })
})
