import { describe, expect, it } from 'vitest'
import { officialFrontendPlugins } from './official'

describe('official frontend plugins', () => {
  it('loads the Connector, AI, and Quant result pages', async () => {
    const modules = await Promise.all(officialFrontendPlugins.map((plugin) => plugin.load()))

    expect(officialFrontendPlugins.map((plugin) => plugin.id)).toEqual([
      'official.ai',
      'official.connector',
      'official.quant'
    ])
    expect((await modules[0].resultPages.calls()).default).toBeDefined()
    expect((await modules[1].resultPages.connections()).default).toBeDefined()
    expect(modules[2].resultPages.quant).toBeTypeOf('function')
  })
})
