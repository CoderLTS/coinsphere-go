import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { compileScript, compileStyle, parse } from '@vue/compiler-sfc'
import { describe, expect, it } from 'vitest'
import * as contractPlugin from '../../../plugins/contract-test/frontend'
import type { FrontendPluginModule } from './sdk'

const fixtureRoot = fileURLToPath(
  new URL('../../../plugins/contract-test/frontend/', import.meta.url)
)

describe('contract plugin result page', () => {
  it('compiles the declared Vue component and style', () => {
    const checkedModule: FrontendPluginModule = contractPlugin
    const filename = `${fixtureRoot}ResultPage.vue`
    const { descriptor, errors } = parse(readFileSync(filename, 'utf8'), { filename })

    expect(errors).toEqual([])
    expect(() => compileScript(descriptor, { id: 'contract-result' })).not.toThrow()
    for (const style of descriptor.styles) {
      expect(
        compileStyle({
          id: 'contract-result',
          filename,
          source: style.content,
          scoped: style.scoped
        }).errors
      ).toEqual([])
    }
    expect(checkedModule.resultPages['contract-run']).toBeTypeOf('function')
  })
})
