package actualize

type SeedDoc struct {
	ID    string
	Title string
	Body  string
}

type ChatCorrection struct {
	Channel       string
	User          string
	Text          string
	TS            string
	CorrectsDocID string
}

type QA struct {
	Question     string
	AnswerBefore string
	AnswerAfter  string
	Affected     bool
	TargetDocID  string
}

func SeedDocs() []SeedDoc {
	return []SeedDoc{
		{ID: "team", Title: "Команда Аврора Роботикс", Body: "Технический директор — Дмитрий Волков. Руководитель разработки ПО — Анна Соколова. Глава производства — Игорь Ковалёв."},
		{ID: "roadmap", Title: "Роадмап AV-3", Body: "Релиз дрона-курьера AV-3 запланирован на 15 марта 2026 года."},
		{ID: "budget", Title: "Бюджет AV-3", Body: "Бюджет проекта AV-3 на 2026 год составляет 42 млн рублей."},
		{ID: "office", Title: "Офис компании", Body: "Головной офис компании находится в Новосибирске, Академгородок."},
		{ID: "partners", Title: "Поставщики", Body: "Основной поставщик аккумуляторов — компания ЭнергоЛит."},
		{ID: "certification", Title: "Сертификация AV-3", Body: "Сертификация дрона AV-3 в Росавиации назначена на май 2026 года."},
		{ID: "hiring", Title: "Вакансии", Body: "Открыта вакансия инженера по авионике, дедлайн подачи заявок — 1 апреля 2026 года."},
		{ID: "safety", Title: "Технические характеристики AV-3", Body: "Максимальная взлётная масса AV-3 — 25 кг, соответствует категории лёгких БАС."},
		{ID: "investors", Title: "Инвесторы", Body: "Раунд A закрыт в декабре 2025 года, ведущий инвестор — фонд «СибВенчур»."},
		{ID: "warranty", Title: "Гарантия", Body: "Гарантийный срок на AV-3 — 24 месяца с даты поставки."},
	}
}

func ChatCorrections() []ChatCorrection {
	return []ChatCorrection{
		{Channel: "C-AVRORA", User: "U-PM", TS: "1780000100.000100", Text: "Важно: релиз AV-3 переносится с 15 марта на 20 июня 2026 — нужно больше времени на сертификационные тесты батареи.", CorrectsDocID: "roadmap"},
		{Channel: "C-AVRORA", User: "U-PM", TS: "1780000200.000100", Text: "Обновление по бюджету: бюджет AV-3 на 2026 год увеличен до 55 млн рублей после закрытия раунда A.", CorrectsDocID: "budget"},
		{Channel: "C-AVRORA", User: "U-PM", TS: "1780000300.000100", Text: "Кадровое: Игорь Ковалёв уходит с позиции главы производства, его сменяет Мария Литвинова с 1 февраля 2026 года.", CorrectsDocID: "team"},
		{Channel: "C-AVRORA", User: "U-PM", TS: "1780000400.000100", Text: "Поставщик аккумуляторов меняется: с апреля переходим с ЭнергоЛит на PowerCell Rus из-за срывов поставок.", CorrectsDocID: "partners"},
		{Channel: "C-AVRORA", User: "U-PM", TS: "1780000500.000100", Text: "Сертификация Росавиации перенесена с мая на август 2026 из-за переноса релиза AV-3.", CorrectsDocID: "certification"},
	}
}

func Questions() []QA {
	return []QA{
		{Question: "Когда запланирован релиз дрона AV-3?", AnswerBefore: "15 марта 2026", AnswerAfter: "20 июня 2026", Affected: true, TargetDocID: "roadmap"},
		{Question: "На какую дату намечен релиз AV-3?", AnswerBefore: "15 марта 2026", AnswerAfter: "20 июня 2026", Affected: true, TargetDocID: "roadmap"},
		{Question: "Какой бюджет проекта AV-3 на 2026 год?", AnswerBefore: "42 млн рублей", AnswerAfter: "55 млн рублей", Affected: true, TargetDocID: "budget"},
		{Question: "Сколько денег заложено на AV-3 в 2026 году?", AnswerBefore: "42 млн рублей", AnswerAfter: "55 млн рублей", Affected: true, TargetDocID: "budget"},
		{Question: "Кто возглавляет производство в Аврора Роботикс?", AnswerBefore: "Игорь Ковалёв", AnswerAfter: "Мария Литвинова", Affected: true, TargetDocID: "team"},
		{Question: "Кто глава производства?", AnswerBefore: "Игорь Ковалёв", AnswerAfter: "Мария Литвинова", Affected: true, TargetDocID: "team"},
		{Question: "Кто основной поставщик аккумуляторов?", AnswerBefore: "ЭнергоЛит", AnswerAfter: "PowerCell Rus", Affected: true, TargetDocID: "partners"},
		{Question: "Какая компания поставляет аккумуляторы для AV-3?", AnswerBefore: "ЭнергоЛит", AnswerAfter: "PowerCell Rus", Affected: true, TargetDocID: "partners"},
		{Question: "Когда назначена сертификация AV-3 в Росавиации?", AnswerBefore: "май 2026", AnswerAfter: "август 2026", Affected: true, TargetDocID: "certification"},
		{Question: "На какой месяц запланирована сертификация в Росавиации?", AnswerBefore: "май 2026", AnswerAfter: "август 2026", Affected: true, TargetDocID: "certification"},
		{Question: "Где находится головной офис компании?", AnswerBefore: "Новосибирск, Академгородок", AnswerAfter: "Новосибирск, Академгородок", Affected: false, TargetDocID: "office"},
		{Question: "Какая максимальная взлётная масса AV-3?", AnswerBefore: "25 кг", AnswerAfter: "25 кг", Affected: false, TargetDocID: "safety"},
		{Question: "Кто ведущий инвестор раунда A?", AnswerBefore: "фонд «СибВенчур»", AnswerAfter: "фонд «СибВенчур»", Affected: false, TargetDocID: "investors"},
		{Question: "Какой гарантийный срок на AV-3?", AnswerBefore: "24 месяца", AnswerAfter: "24 месяца", Affected: false, TargetDocID: "warranty"},
		{Question: "До какого числа принимаются заявки на вакансию инженера по авионике?", AnswerBefore: "1 апреля 2026", AnswerAfter: "1 апреля 2026", Affected: false, TargetDocID: "hiring"},
	}
}
