import { Routes, Route } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './views/Dashboard'
import Inventories from './views/Inventories'
import Runbooks from './views/Runbooks'
import Governance from './views/Governance'
import Credentials from './views/Credentials'
import JobDetails from './views/JobDetails'

function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Dashboard />} />
        <Route path="inventories" element={<Inventories />} />
        <Route path="runbooks" element={<Runbooks />} />
        <Route path="jobs/:id" element={<JobDetails />} />
        <Route path="governance" element={<Governance />} />
        <Route path="credentials" element={<Credentials />} />
      </Route>
    </Routes>
  )
}

export default App
