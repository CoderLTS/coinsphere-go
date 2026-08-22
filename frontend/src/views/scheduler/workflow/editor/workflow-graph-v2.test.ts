import { beforeEach, describe, expect, it } from 'vitest'
import type { WorkflowGraph, WorkflowNodeDefinitionItem } from '@/api/scheduler'
import { syncNodeDefinitions } from './node-registry'
import { mapDomainGraphToServer, mapServerGraphToDomain } from './workflow-editor.mapper'
import { validateWorkflowDraft } from './workflow-editor.validator'

const objectSchema = (propertyType: string) => ({
  type: 'object',
  properties: { value: { type: propertyType } },
  additionalProperties: false
})

const definitions: WorkflowNodeDefinitionItem[] = [
  {
    typeCode: 'start.manual',
    label: '手动开始',
    configSchema: { type: 'object', properties: {} },
    kind: 'start',
    inputPorts: [],
    outputPorts: [{ id: 'result', label: '结果', required: false, schema: objectSchema('string') }],
    executionMode: 'sync',
    securityPolicy: 'standard',
    requiredPermission: ''
  },
  {
    typeCode: 'state.set',
    label: '设置状态',
    configSchema: { type: 'object', properties: {} },
    kind: 'plain',
    inputPorts: [{ id: 'input', label: '输入', required: true, schema: objectSchema('string') }],
    outputPorts: [{ id: 'result', label: '结果', required: false, schema: objectSchema('string') }],
    executionMode: 'sync',
    securityPolicy: 'standard',
    requiredPermission: ''
  },
  {
    typeCode: 'end',
    label: '结束',
    configSchema: { type: 'object', properties: {} },
    kind: 'terminal',
    inputPorts: [],
    outputPorts: [{ id: 'result', label: '结果', required: false, schema: objectSchema('string') }],
    executionMode: 'sync',
    securityPolicy: 'standard',
    requiredPermission: ''
  }
]

const graph = (): WorkflowGraph => ({
  schemaVersion: 2,
  nodes: [
    {
      id: 'start',
      type: 'start.manual',
      label: '开始',
      config: { entryKey: 'manual.default' },
      position: { x: 0, y: 0 }
    },
    { id: 'task', type: 'state.set', label: '处理', config: {}, position: { x: 280, y: 0 } },
    { id: 'end', type: 'end', label: '结束', config: {}, position: { x: 560, y: 0 } }
  ],
  edges: [
    { id: 'flow-1', kind: 'flow', source: 'start', target: 'task' },
    { id: 'flow-2', kind: 'flow', source: 'task', target: 'end' },
    {
      id: 'data-1',
      kind: 'data',
      source: 'start',
      target: 'task',
      sourcePort: 'result',
      targetPort: 'input',
      sourcePointer: '/value',
      targetPointer: '/value'
    }
  ]
})

describe('WorkflowGraphV2', () => {
  beforeEach(() => syncNodeDefinitions(definitions))

  it('round-trips flow and data edges without exposing physical X6 port ids', () => {
    const domain = mapServerGraphToDomain(graph(), definitions, [])
    const saved = mapDomainGraphToServer(domain)

    expect(saved.schemaVersion).toBe(2)
    expect(saved.edges).toMatchObject(graph().edges)
    expect(saved.edges[2]).toMatchObject({
      sourcePort: 'result',
      targetPort: 'input',
      sourcePointer: '/value',
      targetPointer: '/value'
    })
    expect(validateWorkflowDraft(domain)).toEqual([])
  })

  it('rejects data from a node that has not executed yet', () => {
    const invalid = graph()
    invalid.edges[2] = {
      ...invalid.edges[2],
      source: 'end',
      sourcePort: 'result'
    }

    const issues = validateWorkflowDraft(mapServerGraphToDomain(invalid, definitions, []))
    expect(issues.some((issue) => issue.message.includes('之前已经执行'))).toBe(true)
  })

  it('rejects incompatible mapped field types', () => {
    const incompatibleDefinitions = definitions.map((definition) =>
      definition.typeCode === 'state.set'
        ? {
            ...definition,
            inputPorts: [
              { id: 'input', label: '输入', required: true, schema: objectSchema('number') }
            ]
          }
        : definition
    )
    syncNodeDefinitions(incompatibleDefinitions)

    const issues = validateWorkflowDraft(
      mapServerGraphToDomain(graph(), incompatibleDefinitions, [])
    )
    expect(issues.some((issue) => issue.message.includes('类型不兼容'))).toBe(true)
  })

  it('rejects more than one flow successor from a plain node', () => {
    const invalid = graph()
    invalid.edges.push({ id: 'flow-3', kind: 'flow', source: 'task', target: 'start' })

    const issues = validateWorkflowDraft(mapServerGraphToDomain(invalid, definitions, []))
    expect(issues.some((issue) => issue.message.includes('最多只能有一个后继'))).toBe(true)
  })
})
