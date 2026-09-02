import assert from 'node:assert/strict'
import test from 'node:test'
import { talkBiasText } from './format.js'

test('talk bias uses human-readable levels', () => {
  assert.deepEqual([-0.2, 0, 0.2].map(talkBiasText), ['偏低', '中性', '偏高'])
})
