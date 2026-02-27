import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Play, Square, Save, Plus, Loader2, CheckCircle2, XCircle, Wrench, Brain, ChevronDown, ChevronUp, Trash2, ArrowLeft } from 'lucide-react'
import { useLanguage } from '../i18n'
import api from '../api/client'
import Toast from '../components/Toast'

const authFetch = async (url: string, options: RequestInit = {}) => {
  const token = localStorage.getItem('token')
  const headers = {
    ...options.headers,
    ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
  }
  return fetch(url, { ...options, headers })
}

const getApiBase = () => {
  if (import.meta.env.PROD) return '/api/v1'
  const port = import.meta.env.VITE_API_PORT || '8080'
  const host = import.meta.env.VITE_API_HOST || 'localhost'
  return import.meta.env.VITE_API_URL || `http://${host}:${port}/api/v1`
}

interface DisplayEvent {
  type: 'progress' | 'thinking' | 'tool_call' | 'error' | 'script_ready' | 'done'
  content?: string
  data?: any
}

interface ScriptAction {
  type: string
  url?: string
  selector?: string
  xpath?: string
  value?: string
  key?: string
  duration?: number
  tag_name?: string
  text?: string
  remark?: string
  js_code?: string
}

interface GeneratedScript {
  id: string
  name: string
  description: string
  url: string
  actions: ScriptAction[]
}

type Phase = 'config' | 'running' | 'result'

export default function AIExplorer() {
  const { t } = useLanguage()
  const navigate = useNavigate()
  const [phase, setPhase] = useState<Phase>('config')

  // Config state
  const [taskDesc, setTaskDesc] = useState('')
  const [startURL, setStartURL] = useState('')
  const [llmConfigId, setLlmConfigId] = useState('')
  const [llmConfigs, setLlmConfigs] = useState<any[]>([])

  // Running state
  const [sessionId, setSessionId] = useState('')
  const [displayEvents, setDisplayEvents] = useState<DisplayEvent[]>([])
  const [isConnecting, setIsConnecting] = useState(false)

  // Result state
  const [generatedScript, setGeneratedScript] = useState<GeneratedScript | null>(null)
  const [scriptName, setScriptName] = useState('')
  const [isSaving, setIsSaving] = useState(false)

  // UI state
  const [expandedToolCalls, setExpandedToolCalls] = useState<Set<number>>(new Set())
  const [showToast, setShowToast] = useState(false)
  const [toastMessage, setToastMessage] = useState('')
  const [toastType, setToastType] = useState<'success' | 'error' | 'info'>('info')
  const eventsEndRef = useRef<HTMLDivElement>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    api.listLLMConfigs().then(res => {
      setLlmConfigs(res.data?.configs || [])
    }).catch(() => {})
  }, [])

  useEffect(() => {
    eventsEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [displayEvents])

  const toast = (msg: string, type: 'success' | 'error' | 'info' = 'info') => {
    setToastMessage(msg)
    setToastType(type)
    setShowToast(true)
  }

  // Append an SSE event intelligently:
  // - merge consecutive thinking events
  // - merge tool_call events by tool_name (params + result → one entry)
  const appendEvent = useCallback((event: DisplayEvent) => {
    setDisplayEvents(prev => {
      // For 'thinking' type, merge into the last thinking event
      if (event.type === 'thinking' && event.content) {
        const last = prev[prev.length - 1]
        if (last && last.type === 'thinking') {
          const updated = [...prev]
          updated[updated.length - 1] = {
            ...last,
            content: (last.content || '') + (event.content || ''),
          }
          return updated
        }
      }

      // For 'tool_call' type, merge with the previous tool_call of the same tool_name
      if (event.type === 'tool_call' && event.data) {
        const toolName = event.data.tool_name
        const status = event.data.status

        if (toolName && status === 'calling') {
          // New calling event — auto-resolve any previous unresolved 'calling' entries for the same tool
          const updated = prev.map(ev =>
            ev.type === 'tool_call' && ev.data?.tool_name === toolName && ev.data?.status === 'calling'
              ? { ...ev, data: { ...ev.data, status: 'success', message: 'done' } }
              : ev
          )
          return [...updated, event]
        }

        if (toolName && status !== 'calling') {
          // This is a result event — find the matching 'calling' event and merge
          for (let i = prev.length - 1; i >= 0; i--) {
            const existing = prev[i]
            if (
              existing.type === 'tool_call' &&
              existing.data?.tool_name === toolName &&
              existing.data?.status === 'calling'
            ) {
              const updated = [...prev]
              updated[i] = {
                ...existing,
                data: {
                  ...existing.data,
                  status: event.data.status,
                  result: event.data.result,
                  message: event.data.message,
                },
              }
              return updated
            }
          }
          // No matching calling entry found — ignore stale result
          return prev
        }
      }

      return [...prev, event]
    })
  }, [])

  const handleStart = async () => {
    if (!taskDesc.trim()) {
      toast(t('aiExplorer.taskRequired'), 'error')
      return
    }

    setIsConnecting(true)
    setDisplayEvents([])
    setGeneratedScript(null)

    try {
      const res = await authFetch(`${getApiBase()}/ai-explore/start`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          task_desc: taskDesc,
          start_url: startURL,
          llm_config_id: llmConfigId,
        }),
      })
      const data = await res.json()
      if (!res.ok) {
        toast(data.error || 'Failed to start exploration', 'error')
        setIsConnecting(false)
        return
      }

      setSessionId(data.session_id)
      setPhase('running')
      connectSSE(data.session_id)
    } catch (err: any) {
      toast(err.message || 'Failed to start', 'error')
    } finally {
      setIsConnecting(false)
    }
  }

  const connectSSE = (sid: string) => {
    const controller = new AbortController()
    abortRef.current = controller
    const token = localStorage.getItem('token')
    const url = `${getApiBase()}/ai-explore/${sid}/stream`

    fetch(url, {
      headers: token ? { 'Authorization': `Bearer ${token}` } : {},
      signal: controller.signal,
    }).then(async response => {
      const reader = response.body?.getReader()
      if (!reader) return
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          try {
            const event: DisplayEvent = JSON.parse(line.substring(6))
            appendEvent(event)

            if (event.type === 'script_ready' && event.data) {
              const script = typeof event.data === 'string' ? JSON.parse(event.data) : event.data
              setGeneratedScript(script)
              setScriptName(script.name || '')
            }
            if (event.type === 'done') {
              setPhase(prev => prev === 'running' ? 'result' : prev)
            }
          } catch {}
        }
      }
    }).catch(err => {
      if (err.name !== 'AbortError') {
        appendEvent({ type: 'error', content: `Connection error: ${err.message}` })
      }
    })
  }

  const handleStop = async () => {
    if (!sessionId) return
    abortRef.current?.abort()
    try {
      await authFetch(`${getApiBase()}/ai-explore/${sessionId}/stop`, { method: 'POST' })
    } catch {}
    setPhase('result')
  }

  const handleSave = async () => {
    if (!sessionId || !generatedScript) return
    setIsSaving(true)
    try {
      const res = await authFetch(`${getApiBase()}/ai-explore/${sessionId}/save`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: scriptName || generatedScript.name }),
      })
      const data = await res.json()
      if (res.ok) {
        toast(t('aiExplorer.saveSuccess'), 'success')
      } else {
        toast(data.error || 'Save failed', 'error')
      }
    } catch (err: any) {
      toast(err.message, 'error')
    } finally {
      setIsSaving(false)
    }
  }

  const handleReset = () => {
    abortRef.current?.abort()
    setPhase('config')
    setSessionId('')
    setDisplayEvents([])
    setGeneratedScript(null)
    setScriptName('')
  }

  const removeAction = (index: number) => {
    if (!generatedScript) return
    const updated = { ...generatedScript, actions: generatedScript.actions.filter((_, i) => i !== index) }
    setGeneratedScript(updated)
  }

  const toggleToolCall = (index: number) => {
    setExpandedToolCalls(prev => {
      const next = new Set(prev)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })
  }

  const getActionLabel = (action: ScriptAction) => {
    switch (action.type) {
      case 'navigate': return `${t('aiExplorer.action.navigate')}: ${action.url || ''}`
      case 'click': return `${t('aiExplorer.action.click')}: ${action.xpath || action.selector || ''}`
      case 'input': return `${t('aiExplorer.action.input')}: "${action.value || ''}"`
      case 'select': return `${t('aiExplorer.action.select')}: ${action.value || ''}`
      case 'keyboard': return `${t('aiExplorer.action.keyboard')}: ${action.key || ''}`
      case 'scroll': return t('aiExplorer.action.scroll')
      case 'sleep': return `${t('aiExplorer.action.sleep')}: ${action.duration || 0}ms`
      case 'execute_js': return `${t('aiExplorer.action.executeJs')}: ${(action.js_code || '').substring(0, 60)}${(action.js_code || '').length > 60 ? '...' : ''}`
      default: return action.type
    }
  }

  const getToolCallStatusBadge = (data: any) => {
    if (!data) return null
    const status = data.status
    if (status === 'calling') {
      return <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400 ml-2">calling...</span>
    }
    if (status === 'success') {
      return <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 ml-2">done</span>
    }
    if (status === 'error') {
      return <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-red-50 dark:bg-red-900/20 text-red-500 dark:text-red-400 ml-2">error</span>
    }
    return null
  }

  // ---- Render ----
  return (
    <div>
      {/* Page header */}
      <div className="mb-8">
        <div className="flex items-center justify-between">
          <div>
            <div className="flex items-center gap-3 mb-2">
              <button
                onClick={() => navigate('/scripts')}
                className="p-1.5 rounded-lg text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                title={t('script.title')}
              >
                <ArrowLeft className="w-5 h-5" />
              </button>
              <h1 className="text-2xl lg:text-3xl font-bold text-gray-900 dark:text-gray-100">{t('aiExplorer.title')}</h1>
            </div>
            <p className="text-[15px] text-gray-600 dark:text-gray-400 ml-10">{t('aiExplorer.subtitle')}</p>
          </div>
          {phase !== 'config' && (
            <button
              onClick={handleReset}
              className="btn-secondary flex items-center gap-1.5"
            >
              <Plus className="w-3.5 h-3.5" />
              <span>{t('aiExplorer.newExplore')}</span>
            </button>
          )}
        </div>
      </div>

      {/* Phase 1: Config */}
      {phase === 'config' && (
        <div className="space-y-6 max-w-2xl">
          <div>
            <label className="block text-[15px] font-medium text-gray-700 dark:text-gray-300 mb-2">{t('aiExplorer.taskDesc')}</label>
            <textarea
              value={taskDesc}
              onChange={e => setTaskDesc(e.target.value)}
              placeholder={t('aiExplorer.taskDescPlaceholder')}
              rows={5}
              className="w-full px-4 py-3 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-gray-400 focus:outline-none resize-none text-sm leading-relaxed"
            />
          </div>

          <div>
            <label className="block text-[15px] font-medium text-gray-700 dark:text-gray-300 mb-2">{t('aiExplorer.startURL')}</label>
            <input
              value={startURL}
              onChange={e => setStartURL(e.target.value)}
              placeholder="https://example.com"
              className="w-full px-4 py-3 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-gray-400 focus:outline-none text-sm"
            />
          </div>

          <div>
            <label className="block text-[15px] font-medium text-gray-700 dark:text-gray-300 mb-2">{t('aiExplorer.llmSelect')}</label>
            <select
              value={llmConfigId}
              onChange={e => setLlmConfigId(e.target.value)}
              className="w-full px-4 py-3 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-gray-400 focus:outline-none text-sm"
            >
              <option value="">{t('aiExplorer.llmDefault')}</option>
              {llmConfigs.map(cfg => (
                <option key={cfg.id} value={cfg.id}>{cfg.name || cfg.model}</option>
              ))}
            </select>
          </div>

          <div className="pt-2">
            <button
              onClick={handleStart}
              disabled={isConnecting || !taskDesc.trim()}
              className="flex items-center gap-2 px-6 py-3 bg-gray-900 dark:bg-gray-100 text-white dark:text-gray-900 rounded-lg hover:bg-gray-800 dark:hover:bg-gray-200 disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
            >
              {isConnecting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
              {t('aiExplorer.startExplore')}
            </button>
          </div>
        </div>
      )}

      {/* Phase 2: Running */}
      {phase === 'running' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-400">
              <Loader2 className="w-4 h-4 animate-spin" />
              {t('aiExplorer.exploring')}
            </div>
            <button
              onClick={handleStop}
              className="flex items-center gap-1.5 px-3 py-1.5 border border-gray-300 dark:border-gray-600 rounded-lg text-sm text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
            >
              <Square className="w-3.5 h-3.5" />
              {t('aiExplorer.stopExplore')}
            </button>
          </div>

          <div className="border border-gray-200 dark:border-gray-700 rounded-lg bg-gray-50 dark:bg-gray-900 max-h-[520px] overflow-y-auto">
            <div className="p-4 space-y-3 text-sm">
              {displayEvents.map((ev, i) => (
                <div key={i} className="flex items-start gap-2.5">
                  {ev.type === 'progress' && <Brain className="w-4 h-4 mt-0.5 text-gray-400 shrink-0" />}
                  {ev.type === 'thinking' && <Brain className="w-4 h-4 mt-0.5 text-blue-400 shrink-0" />}
                  {ev.type === 'tool_call' && (
                    <button onClick={() => toggleToolCall(i)} className="shrink-0">
                      <Wrench className="w-4 h-4 mt-0.5 text-gray-400" />
                    </button>
                  )}
                  {ev.type === 'error' && <XCircle className="w-4 h-4 mt-0.5 text-red-500 shrink-0" />}
                  {ev.type === 'script_ready' && <CheckCircle2 className="w-4 h-4 mt-0.5 text-green-500 shrink-0" />}
                  {ev.type === 'done' && <CheckCircle2 className="w-4 h-4 mt-0.5 text-green-500 shrink-0" />}
                  <div className="min-w-0 flex-1">
                    {ev.type === 'tool_call' ? (
                      <div>
                        <div
                          onClick={() => toggleToolCall(i)}
                          className="cursor-pointer text-gray-600 dark:text-gray-300 flex items-center gap-1 text-xs"
                        >
                          <span className="font-medium">{ev.data?.tool_name || 'unknown'}</span>
                          {getToolCallStatusBadge(ev.data)}
                          {expandedToolCalls.has(i) ? <ChevronUp className="w-3 h-3 ml-1" /> : <ChevronDown className="w-3 h-3 ml-1" />}
                        </div>
                        {expandedToolCalls.has(i) && ev.data && (
                          <div className="mt-1.5 space-y-1.5">
                            {ev.data.arguments && Object.keys(ev.data.arguments).length > 0 && (
                              <pre className="text-xs text-gray-500 dark:text-gray-400 whitespace-pre-wrap break-all bg-gray-100 dark:bg-gray-800 rounded p-2 font-mono">
                                <span className="text-gray-400 dark:text-gray-500">args: </span>{JSON.stringify(ev.data.arguments, null, 2)}
                              </pre>
                            )}
                            {ev.data.result && (
                              <pre className="text-xs text-green-600 dark:text-green-400 whitespace-pre-wrap break-all bg-green-50 dark:bg-green-900/20 rounded p-2 font-mono max-h-[150px] overflow-y-auto">
                                <span className="text-green-500 dark:text-green-600">result: </span>{typeof ev.data.result === 'string' ? ev.data.result.substring(0, 500) : JSON.stringify(ev.data.result, null, 2).substring(0, 500)}
                              </pre>
                            )}
                          </div>
                        )}
                      </div>
                    ) : (
                      <span className={`${ev.type === 'error' ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-300'} whitespace-pre-wrap break-words leading-relaxed`}>
                        {ev.content || (ev.type === 'done' ? t('aiExplorer.done') : '')}
                      </span>
                    )}
                  </div>
                </div>
              ))}
              <div ref={eventsEndRef} />
            </div>
          </div>
        </div>
      )}

      {/* Phase 3: Result */}
      {phase === 'result' && (
        <div className="space-y-5">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t('aiExplorer.result')}</h2>
          </div>

          {/* Event log (collapsible) */}
          {displayEvents.length > 0 && (
            <details className="border border-gray-200 dark:border-gray-700 rounded-lg">
              <summary className="px-4 py-2.5 text-sm font-medium text-gray-600 dark:text-gray-400 cursor-pointer select-none">
                {t('aiExplorer.eventLog')} ({displayEvents.length})
              </summary>
              <div className="max-h-[200px] overflow-y-auto px-4 pb-3 space-y-1 text-xs font-mono text-gray-500 dark:text-gray-400">
                {displayEvents.map((ev, i) => (
                  <div key={i}>[{ev.type}] {ev.content || (ev.type === 'tool_call' ? ev.data?.tool_name : '')}</div>
                ))}
              </div>
            </details>
          )}

          {generatedScript ? (
            <div className="space-y-5">
              <div>
                <label className="block text-[15px] font-medium text-gray-700 dark:text-gray-300 mb-2">{t('aiExplorer.scriptName')}</label>
                <input
                  value={scriptName}
                  onChange={e => setScriptName(e.target.value)}
                  className="w-full px-4 py-3 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 focus:ring-2 focus:ring-gray-400 focus:outline-none text-sm"
                />
              </div>

              <div className="border border-gray-200 dark:border-gray-700 rounded-lg">
                <div className="px-4 py-2.5 border-b border-gray-200 dark:border-gray-700 flex items-center justify-between">
                  <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {t('aiExplorer.actions')} ({generatedScript.actions.length})
                  </span>
                </div>
                <div className="divide-y divide-gray-100 dark:divide-gray-800 max-h-[400px] overflow-y-auto">
                  {generatedScript.actions.map((action, i) => (
                    <div key={i} className="px-4 py-3 flex items-center justify-between group hover:bg-gray-50 dark:hover:bg-gray-800/50">
                      <div className="flex items-center gap-3 min-w-0">
                        <span className="text-xs font-mono text-gray-400 w-6 text-right shrink-0">{i + 1}</span>
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-300 shrink-0">
                          {action.type}
                        </span>
                        <span className="text-sm text-gray-600 dark:text-gray-400 truncate">
                          {getActionLabel(action)}
                        </span>
                      </div>
                      <button
                        onClick={() => removeAction(i)}
                        className="opacity-0 group-hover:opacity-100 p-1 text-gray-400 hover:text-red-500 transition-all"
                        title={t('common.delete')}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  ))}
                  {generatedScript.actions.length === 0 && (
                    <div className="px-4 py-8 text-center text-sm text-gray-400">
                      {t('aiExplorer.noActions')}
                    </div>
                  )}
                </div>
              </div>

              <button
                onClick={handleSave}
                disabled={isSaving || generatedScript.actions.length === 0}
                className="flex items-center gap-2 px-6 py-3 bg-gray-900 dark:bg-gray-100 text-white dark:text-gray-900 rounded-lg hover:bg-gray-800 dark:hover:bg-gray-200 disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
              >
                {isSaving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}
                {t('aiExplorer.saveScript')}
              </button>
            </div>
          ) : (
            <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-8 text-center">
              <XCircle className="w-8 h-8 mx-auto text-gray-300 dark:text-gray-600 mb-2" />
              <p className="text-sm text-gray-500 dark:text-gray-400">{t('aiExplorer.noScript')}</p>
            </div>
          )}
        </div>
      )}

      {showToast && (
        <Toast message={toastMessage} type={toastType} onClose={() => setShowToast(false)} />
      )}
    </div>
  )
}
