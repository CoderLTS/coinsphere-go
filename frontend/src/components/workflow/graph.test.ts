import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addNode, canvasCells, cloneGraph, removeNode } from './graph'
import type { WorkflowGraph, WorkflowNodeDefinition } from '../../api/workflows'

const fixture: WorkflowGraph = {
  schemaVersion: 3,
  entryPoints: { main: 'events', manualReview: 'review' },
  nodes: [
    {
      nodeInstanceId: 'events',
      nodeType: 'core.event',
      nodeVersion: '1.0.0',
      config: {
        types: ['business.created', 'business.updated'],
        source: 'urn:business',
        subject: 'case',
        partitionKey: 'event.subject'
      },
      position: { x: 33, y: 74 }
    },
    {
      nodeInstanceId: 'review',
      nodeType: 'test.business.review',
      nodeVersion: '2.0.0',
      config: { enabled: false },
      inputBindings: {
        source: { kind: 'field', nodeInstanceId: 'events', fieldPath: ['items', '0', 'name'] },
        job: { kind: 'input', fieldPath: ['job'] },
        fixed: { kind: 'literal', value: { labels: ['a', 'b'] } },
        expression: { kind: 'cel', expression: 'nodes.events.items[input.index].name' }
      },
      position: { x: 333, y: 174 }
    },
    {
      nodeInstanceId: 'loop',
      nodeType: 'core.loop',
      nodeVersion: '1.0.0',
      config: {
        maxIterations: 4,
        timeoutSeconds: 30,
        exitCondition: 'input.iteration == 3',
        body: {
          schemaVersion: 3,
          entryPoints: {},
          nodes: [
            {
              nodeInstanceId: 'item',
              nodeType: 'core.loop_item',
              nodeVersion: '1.0.0',
              config: {},
              position: { x: 1, y: 2 }
            }
          ],
          edges: []
        }
      },
      position: { x: 533, y: 174 }
    }
  ],
  edges: [
    {
      edgeId: 'incoming-review',
      sourceNodeInstanceId: 'events',
      sourcePort: 'accepted',
      targetNodeInstanceId: 'review',
      targetPort: 'in',
      condition: 'nodes.events.ready && event.type in ["business.created", "business.updated"]'
    }
  ]
}

test('editing and JSON roundtrip preserve the complete native definition', () => {
  const edited = cloneGraph(fixture)
  edited.nodes[1].position!.x = 444
  const saved = JSON.parse(JSON.stringify(edited)) as WorkflowGraph
  assert.deepEqual(saved.entryPoints, fixture.entryPoints)
  assert.deepEqual(
    saved.nodes.map((node) => ({ ...node, position: undefined })),
    fixture.nodes.map((node) => ({ ...node, position: undefined }))
  )
  assert.deepEqual(saved.edges, fixture.edges)
  assert.equal(fixture.nodes[1].position?.x, 333)
  const cells = canvasCells(saved, [], { review: 'waiting', loop: 'retrying' })
  const review = cells.find((cell) => cell.id === 'review')
  assert.ok(review && 'label' in review && review.label.includes('waiting'))
  const incoming = cells.find((cell) => cell.id === 'incoming-review')
  assert.deepEqual(incoming && 'source' in incoming ? incoming.source : undefined, {
    cell: 'events',
    port: 'out:accepted'
  })
})

test('adding defaults and deleting an entry leave other definitions untouched', () => {
  const definition = {
    type: 'test.business.action',
    version: '3.0.0',
    kind: 'action',
    configSchema: {
      properties: {
        list: { default: ['one'] },
        credential: { type: 'string', 'x-coinsphere-secret': true }
      }
    }
  } as unknown as WorkflowNodeDefinition
  const next = addNode(fixture, definition)
  assert.deepEqual(next.nodes.slice(0, -1), fixture.nodes)
  assert.deepEqual(next.nodes.at(-1)?.config, { list: ['one'] })
  const removed = removeNode(next, 'events')
  assert.deepEqual(removed.entryPoints, { manualReview: 'review' })
  assert.equal(removed.edges.length, 0)
  assert.deepEqual(removed.nodes[0].inputBindings, fixture.nodes[1].inputBindings)
})
