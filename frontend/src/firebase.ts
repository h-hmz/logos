import { initializeApp } from 'firebase/app'
import { getAnalytics, isSupported } from 'firebase/analytics'
import { getAuth, GoogleAuthProvider } from 'firebase/auth'
import type { Auth } from 'firebase/auth'

export const firebaseEnabled = Boolean(import.meta.env.VITE_FIREBASE_API_KEY)

export let auth: Auth | null = null
export const googleProvider = new GoogleAuthProvider()

if (firebaseEnabled) {
  const app = initializeApp({
    apiKey: import.meta.env.VITE_FIREBASE_API_KEY as string,
    authDomain: import.meta.env.VITE_FIREBASE_AUTH_DOMAIN as string,
    projectId: import.meta.env.VITE_FIREBASE_PROJECT_ID as string,
    appId: import.meta.env.VITE_FIREBASE_APP_ID as string | undefined,
    measurementId: import.meta.env.VITE_FIREBASE_MEASUREMENT_ID as string | undefined,
  })
  auth = getAuth(app)

  if (import.meta.env.VITE_FIREBASE_APP_ID) {
    // Page views, sessions, and engagement are auto-collected once initialized.
    isSupported()
      .then((ok) => {
        if (ok) getAnalytics(app)
      })
      .catch(() => {})
  }
}
