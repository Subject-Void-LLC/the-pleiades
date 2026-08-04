import { NavLink, Outlet } from 'react-router-dom'

export default function Layout() {
  return (
    <div className="layout-container">
      <nav className="sidebar" aria-label="Main Navigation">
        <div className="sidebar-header">
          <h2>PLEIADES</h2>
          <span className="version">v0.1.0-alpha</span>
        </div>
        
        <ul className="nav-links">
          <li>
            <NavLink to="/" end className={({ isActive }) => isActive ? 'active' : ''}>
              [1] DASHBOARD
            </NavLink>
          </li>
          <li>
            <NavLink to="/inventories" className={({ isActive }) => isActive ? 'active' : ''}>
              [2] INVENTORIES
            </NavLink>
          </li>
          <li>
            <NavLink to="/runbooks" className={({ isActive }) => isActive ? 'active' : ''}>
              [3] RUNBOOKS & JOBS
            </NavLink>
          </li>
          <li>
            <NavLink to="/jobs/123" className={({ isActive }) => isActive ? 'active' : ''}>
              [~] LIVE LOG STREAM
            </NavLink>
          </li>
          <li>
            <NavLink to="/governance" className={({ isActive }) => isActive ? 'active' : ''}>
              [4] GOVERNANCE GATES
            </NavLink>
          </li>
          <li>
            <NavLink to="/credentials" className={({ isActive }) => isActive ? 'active' : ''}>
              [5] CREDENTIALS
            </NavLink>
          </li>
        </ul>
      </nav>

      <main className="main-content" role="main">
        <Outlet />
      </main>
    </div>
  )
}
