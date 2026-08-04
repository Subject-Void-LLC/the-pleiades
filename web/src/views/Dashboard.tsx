export default function Dashboard() {
  return (
    <div className="view-container">
      <header className="header">
        <h1>DASHBOARD</h1>
      </header>
      <div className="view-content">
        
        <div className="editorial-grid">
          <div className="primary-column">
            <section className="metrics-grid">
              <div className="metric-card">
                <h3>ACTIVE HOSTS</h3>
                <span className="big-number" style={{color: 'var(--status-ok)'}}>1,024</span>
              </div>
              <div className="metric-card">
                <h3>QUARANTINED</h3>
                <span className="big-number" style={{color: 'var(--status-failed)'}}>12</span>
              </div>
              <div className="metric-card">
                <h3>RUNNER HEALTH</h3>
                <span className="big-number" style={{color: 'var(--status-ok)'}}>ONLINE</span>
              </div>
            </section>
            
            <h2>RECENT ACTIVITY</h2>
            <p>System events, job launches, and sync plugin outputs stream here.</p>
          </div>
          
          <div className="secondary-column">
            <h3>QUICK ACTIONS</h3>
            <p>Trigger a full system sync, review quarantined devices, or manage governance gates.</p>
            
            <h3 style={{marginTop: '2rem'}}>SYSTEM LOAD</h3>
            <p>NATS JetStream: Healthy</p>
            <p>Active Agents: 450</p>
            <p>Pending Jobs: 2</p>
          </div>
        </div>

      </div>
    </div>
  )
}
