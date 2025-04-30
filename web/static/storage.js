// Функция для сохранения данных пользователя
function saveUserData(data) {
    try {
        localStorage.setItem('chatUserData', JSON.stringify(data));
        return true;
    } catch (e) {
        console.error('Ошибка сохранения данных:', e);
        return false;
    }
}

// Функция для загрузки данных пользователя
function loadUserData() {
    try {
        const data = localStorage.getItem('chatUserData');
        return data ? JSON.parse(data) : null;
    } catch (e) {
        console.error('Ошибка загрузки данных:', e);
        return null;
    }
}

// Функция для сохранения списка получателей
function saveRecipients(recipients) {
    try {
        localStorage.setItem('chatRecipients', JSON.stringify(recipients));
        return true;
    } catch (e) {
        console.error('Ошибка сохранения списка получателей:', e);
        return false;
    }
}

// Функция для загрузки списка получателей
function loadRecipients() {
    try {
        const data = localStorage.getItem('chatRecipients');
        return data ? JSON.parse(data) : [];
    } catch (e) {
        console.error('Ошибка загрузки списка получателей:', e);
        return [];
    }
}

// Функция для добавления нового получателя
function addRecipient(recipient) {
    if (!recipient) return false;
    
    const recipients = loadRecipients();
    if (!recipients.includes(recipient)) {
        recipients.push(recipient);
        return saveRecipients(recipients);
    }
    return true;
} 