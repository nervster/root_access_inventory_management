import { SignIn, SignUp } from '@clerk/react'
import { Route, Routes } from 'react-router'
import { NotFound, RequireMembership, RequirePlatformAdmin, RequireSignIn } from './components/Guards'
import { Layout } from './components/Layout'
import { AuthPage } from './pages/AuthPage'
import { HomePage } from './pages/HomePage'
import { BatchPage } from './pages/org/BatchPage'
import { BatchesPage } from './pages/org/BatchesPage'
import { DashboardPage } from './pages/org/DashboardPage'
import { LabelsPage } from './pages/org/LabelsPage'
import { PlantPage } from './pages/org/PlantPage'
import { ProductsPage } from './pages/org/ProductsPage'
import { ReceiveBatchPage } from './pages/org/ReceiveBatchPage'
import { PhotoSessionPage } from './pages/org/PhotoSessionPage'
import { ScanPage } from './pages/org/ScanPage'
import { PotSizesPage } from './pages/org/PotSizesPage'
import { OrdersPage } from './pages/org/OrdersPage'
import { SettingsPage } from './pages/org/SettingsPage'
import { TeamPage } from './pages/org/TeamPage'
import { OrganizationDetailPage } from './pages/platform/OrganizationDetailPage'
import { OrganizationsPage } from './pages/platform/OrganizationsPage'
import { UsersPage } from './pages/platform/UsersPage'

export default function App() {
  return (
    <Routes>
      <Route
        path="/sign-in/*"
        element={
          <AuthPage>
            {/* Sign-up is invite-only, so hide the "Don't have an account?" link. */}
            <SignIn routing="path" path="/sign-in" signUpUrl="/sign-up" appearance={{ elements: { footerAction: { display: 'none' } } }} />
          </AuthPage>
        }
      />
      {/* Clerk invitation emails link here with a ticket; SignUp accepts it. */}
      <Route
        path="/sign-up/*"
        element={
          <AuthPage>
            <SignUp routing="path" path="/sign-up" signInUrl="/sign-in" />
          </AuthPage>
        }
      />
      <Route element={<RequireSignIn />}>
        {/* Outside the layout so only the labels print. */}
        <Route path="orgs/:orgId/batches/:batchId/labels" element={<LabelsPage />} />
        <Route element={<Layout />}>
          <Route index element={<HomePage />} />
          <Route path="orgs/:orgId" element={<RequireMembership />}>
            <Route index element={<DashboardPage />} />
            <Route path="batches" element={<BatchesPage />} />
            <Route path="batches/new" element={<ReceiveBatchPage />} />
            <Route path="batches/:batchId" element={<BatchPage />} />
            <Route path="scan" element={<ScanPage />} />
            <Route path="scan/photos" element={<PhotoSessionPage />} />
            <Route path="orders" element={<OrdersPage />} />
            <Route path="plants/:barcode" element={<PlantPage />} />
            <Route path="products" element={<ProductsPage />} />
            <Route path="pot-sizes" element={<PotSizesPage />} />
            <Route path="team" element={<TeamPage />} />
            <Route path="settings" element={<SettingsPage />} />
          </Route>
          <Route path="platform" element={<RequirePlatformAdmin />}>
            <Route index element={<OrganizationsPage />} />
            <Route path="orgs/:id" element={<OrganizationDetailPage />} />
            <Route path="users" element={<UsersPage />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}
