import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import { AuthProvider } from "@/contexts/AuthContext";
import ProtectedRoute from "@/components/ProtectedRoute";
import Login from "@/pages/Login";
import Main from "@/pages/Main";
import Customers from "@/pages/Customers";
import CustomerDetail from "@/pages/CustomerDetail";
import Accounts from "@/pages/Accounts";
import AccountDetail from "@/pages/AccountDetail";
import Catalog from "@/pages/Catalog";
import PlanDetail from "@/pages/PlanDetail";
import PlanEditor from "@/pages/PlanEditor";
import Ambassadors from "@/pages/Ambassadors";
import Revenue from "@/pages/Revenue";
import RevenueAccount from "@/pages/RevenueAccount";
import Support from "@/pages/Support";
import SupportTicket from "@/pages/SupportTicket";
import Infrastructure from "@/pages/Infrastructure";
import Administration from "@/pages/Administration";
import Reports from "@/pages/Reports";
import Settings from "@/pages/Settings";

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route
            path="/"
            element={
              <ProtectedRoute>
                <Main />
              </ProtectedRoute>
            }
          />
          <Route
            path="/customers"
            element={
              <ProtectedRoute>
                <Customers />
              </ProtectedRoute>
            }
          />
          <Route
            path="/customers/:id"
            element={
              <ProtectedRoute>
                <CustomerDetail />
              </ProtectedRoute>
            }
          />
          <Route
            path="/accounts"
            element={
              <ProtectedRoute>
                <Accounts />
              </ProtectedRoute>
            }
          />
          <Route
            path="/accounts/:id"
            element={
              <ProtectedRoute>
                <AccountDetail />
              </ProtectedRoute>
            }
          />
          <Route
            path="/catalog"
            element={
              <ProtectedRoute>
                <Catalog />
              </ProtectedRoute>
            }
          />
          <Route
            path="/catalog/new"
            element={
              <ProtectedRoute>
                <PlanEditor />
              </ProtectedRoute>
            }
          />
          <Route
            path="/catalog/:id"
            element={
              <ProtectedRoute>
                <PlanDetail />
              </ProtectedRoute>
            }
          />
          <Route
            path="/catalog/:id/edit"
            element={
              <ProtectedRoute>
                <PlanEditor />
              </ProtectedRoute>
            }
          />
          <Route
            path="/ambassadors"
            element={
              <ProtectedRoute>
                <Ambassadors />
              </ProtectedRoute>
            }
          />
          <Route
            path="/revenue"
            element={
              <ProtectedRoute>
                <Revenue />
              </ProtectedRoute>
            }
          />
          <Route
            path="/revenue/accounts/:id"
            element={
              <ProtectedRoute>
                <RevenueAccount />
              </ProtectedRoute>
            }
          />
          <Route
            path="/support"
            element={
              <ProtectedRoute>
                <Support />
              </ProtectedRoute>
            }
          />
          <Route
            path="/support/:id"
            element={
              <ProtectedRoute>
                <SupportTicket />
              </ProtectedRoute>
            }
          />
          <Route
            path="/infrastructure"
            element={
              <ProtectedRoute>
                <Infrastructure />
              </ProtectedRoute>
            }
          />
          <Route
            path="/administration"
            element={
              <ProtectedRoute>
                <Administration />
              </ProtectedRoute>
            }
          />
          <Route
            path="/reports"
            element={
              <ProtectedRoute>
                <Reports />
              </ProtectedRoute>
            }
          />
          {/* Unknown routes fall back to Main (which redirects to /login if needed). */}
          <Route
            path="/settings"
            element={
              <ProtectedRoute>
                <Settings />
              </ProtectedRoute>
            }
          />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  );
}
