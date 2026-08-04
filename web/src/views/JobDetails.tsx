import { useState, useEffect, useRef } from 'react'
import '../index.css'

interface LogEvent {
  task?: string;
  host?: string;
  status?: string;
  event?: string;
  timestamp?: string;
  event_data?: {
    message?: string;
  };
}

export default function JobDetails() {
  const [events, setEvents] = useState<LogEvent[]>([])
  const [search, setSearch] = useState('')
  const [connected, setConnected] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const viewerRef = useRef<HTMLDivElement>(null)
  
  // Use a hardcoded job ID for the scaffold
  const jobId = "123"

  useEffect(() => {
    // Connect to the Go API SSE endpoint
    const evtSource = new EventSource(`http://localhost:8081/api/v1/jobs/${jobId}/logs`)
    
    evtSource.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data) as LogEvent
        setEvents(prev => [...prev, data])
      } catch {
        console.log("Raw event:", e.data)
      }
    }

    evtSource.addEventListener('init', (e: any) => {
      setConnected(true)
      console.log(e.data)
    })

    evtSource.onerror = (err) => {
      console.error("SSE Error:", err)
      setConnected(false)
    }

    return () => evtSource.close()
  }, [])

  // Auto-scroll logic
  useEffect(() => {
    // Respect user's reduced motion preference automatically
    const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    
    if (viewerRef.current && autoScroll && !prefersReducedMotion) {
      viewerRef.current.scrollTop = viewerRef.current.scrollHeight
    }
  }, [events, autoScroll])

  const filteredEvents = events.filter(e => {
    if (!search) return true
    const term = search.toLowerCase()
    return (
      (e.task && e.task.toLowerCase().includes(term)) ||
      (e.host && e.host.toLowerCase().includes(term)) ||
      (e.status && e.status.toLowerCase().includes(term)) ||
      (e.event && e.event.toLowerCase().includes(term))
    )
  })

  return (
    <div className="view-container">
      <header className="header" role="banner">
        <h1>[~] PLEIADES // LOG_STREAM</h1>
        
        <section className="controls" aria-label="Log Stream Controls">
          <span 
            className="stats" 
            role="status" 
            aria-live="polite"
            style={{ color: connected ? 'var(--status-ok)' : 'var(--status-failed)' }}
          >
            {connected ? '● LIVE' : '○ DISCONNECTED'} | {filteredEvents.length} EVENTS
          </span>
          
          <button 
            className="toggle-button"
            onClick={() => setAutoScroll(!autoScroll)}
            aria-pressed={!autoScroll}
            aria-label={autoScroll ? "Pause automatic scrolling" : "Resume automatic scrolling"}
          >
            {autoScroll ? 'PAUSE SCROLL' : 'RESUME SCROLL'}
          </button>

          <input 
            type="search" 
            className="search-input" 
            placeholder="Search tasks, hosts, status..." 
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Filter logs by keyword"
          />
        </section>
      </header>

      {/* Visually hidden aria-live region to announce new logs to screen readers without forcing focus */}
      <div className="visually-hidden" aria-live="polite" aria-atomic="false">
        {events.length > 0 ? `New log event: ${events[events.length - 1].task || events[events.length - 1].event}` : 'Waiting for job data'}
      </div>

      <main className="log-viewer" ref={viewerRef} role="main" aria-label="Log Stream Viewer">
        {filteredEvents.map((evt, idx) => (
          <article key={idx} className="log-event" tabIndex={0}>
            <span className="log-time">{evt.timestamp || new Date().toISOString()}</span>
            <span className={`log-status ${evt.status || 'unknown'}`}>{evt.status || 'OK'}</span>
            <span className="log-host">{evt.host || 'system'}</span>
            <span className="log-task">
              {evt.task ? evt.task : evt.event} 
              {evt.event_data && evt.event_data.message && ` - ${evt.event_data.message}`}
            </span>
          </article>
        ))}
        {events.length === 0 && (
          <div style={{ color: '#555', padding: '2rem' }} role="status">Waiting for job data...</div>
        )}
      </main>
    </div>
  )
}
