import { Route, Routes, Navigate } from 'react-router-dom'
import { ToastProvider } from '@/hooks/use-toast'
import { Toaster } from '@/components/ui/toaster'
import { AuthProvider } from '@/components/AuthProvider'
import { ProtectedRoute } from '@/components/ProtectedRoute'
import { Login } from '@/pages/Login'
import { Dashboard } from '@/pages/Dashboard'
import { RegistryRoles } from '@/pages/RegistryRoles'
import { StubPage } from '@/pages/StubPage'

export default function App() {
  return (
    <ToastProvider>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <Dashboard />
              </ProtectedRoute>
            }
          />
          <Route
            path="/registry/roles"
            element={
              <ProtectedRoute>
                <RegistryRoles />
              </ProtectedRoute>
            }
          />
          <Route
            path="/registry/teams"
            element={
              <ProtectedRoute>
                <StubPage title="Teams" />
              </ProtectedRoute>
            }
          />
          <Route
            path="/registry/processes"
            element={
              <ProtectedRoute>
                <StubPage title="Processes" />
              </ProtectedRoute>
            }
          />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
        <Toaster />
      </AuthProvider>
    </ToastProvider>
  )
}
