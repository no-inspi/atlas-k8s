// Éditeur Monaco en lecture seule pour l'onglet YAML. Chargé à la demande
// (import dynamique) : il ne pèse rien tant que l'onglet n'est pas ouvert.
// Seuls le cœur de l'éditeur et la coloration YAML sont embarqués.
import * as monaco from 'monaco-editor/editor/editor.api'
import 'monaco-editor/languages/definitions/yaml/register'
import 'monaco-editor/editor/contrib/folding/browser/folding'
import EditorWorker from 'monaco-editor/editor/editor.worker?worker'
import { useEffect, useRef } from 'react'

// Le worker est servi par le backend lui-même (CSP worker-src 'self').
self.MonacoEnvironment = { getWorker: () => new EditorWorker() }

export default function MonacoYaml({ value, dark }: { value: string; dark: boolean }) {
  const host = useRef<HTMLDivElement>(null)
  const editor = useRef<monaco.editor.IStandaloneCodeEditor | null>(null)

  useEffect(() => {
    editor.current = monaco.editor.create(host.current!, {
      value: '', language: 'yaml', readOnly: true, domReadOnly: true,
      minimap: { enabled: false }, automaticLayout: true, scrollBeyondLastLine: false,
      fontFamily: 'JetBrains Mono, ui-monospace, monospace', fontSize: 12, lineNumbersMinChars: 3,
      folding: true, showFoldingControls: 'always', renderLineHighlight: 'none',
    })
    return () => editor.current?.dispose()
  }, [])

  useEffect(() => monaco.editor.setTheme(dark ? 'vs-dark' : 'vs'), [dark])

  useEffect(() => {
    const ed = editor.current
    if (!ed) return
    ed.setValue(value)
    // Le bloc status est replié par défaut (spec) : on lit d'abord la configuration.
    const line = value.split('\n').findIndex((l) => l.startsWith('status:'))
    if (line >= 0) {
      setTimeout(() => ed.trigger('atlas', 'editor.fold', { levels: 1, direction: 'down', selectionLines: [line] }), 0)
    }
    ed.setScrollTop(0)
  }, [value])

  return <div className="monaco-host" ref={host} data-testid="yaml-editor" />
}
