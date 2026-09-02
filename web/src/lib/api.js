import axios from 'axios';
export const api = axios.create({
    baseURL: '/admin/api',
    timeout: 5000,
});
export async function getSnapshot(groupID, token) {
    const response = await api.get('/snapshot', {
        params: groupID ? { group_id: groupID } : undefined,
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    });
    return response.data;
}
