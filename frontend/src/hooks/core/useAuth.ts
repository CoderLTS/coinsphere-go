import { useUserStore } from '@/store/modules/user'

export const useAuth = () => {
  const userStore = useUserStore()
  const hasAuth = (permissionCode: string) =>
    (userStore.info?.permissions ?? []).includes(permissionCode)
  return { hasAuth }
}
