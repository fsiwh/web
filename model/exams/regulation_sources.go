package exams

// This reviewed reference catalogue is intentionally independent of archive
// years. A match needs the exact study program, degree and stored PO label.
// Sources were checked against WHS pages and PDF contents on 2026-10-25.
type RegulationDocument struct {
	Title, URL, Date string
}

type RegulationSource struct {
	Program, Degree, Version                   string
	PO, Handbook                               RegulationDocument
	Amendments                                 []RegulationDocument
	SourceURL, HandbookSourceURL, FrameworkURL string
	Note                                       string
	Additional                                 []RegulationDocument
}

const regulationsChecked = "2026-10-25"

func (s RegulationSource) CheckedAt() string { return regulationsChecked }

const whsInfo = "https://www.w-hs.de/informatik/info-center/"
const whsFiles = "https://www.w-hs.de/fileadmin/Oeffentlich/"

func officialNotice(year, filename string) string {
	return whsFiles + "Praesidium/Amtsblatt/Amtliche_Mitteilung_" + year + "/" + filename
}

func regulationSources() []RegulationSource {
	am2024 := officialNotice("2024", "Amtsblatt_2024_Nr_34_vom_171224.pdf")
	amMarch2025 := officialNotice("2025", "AM_2025_04_vom_18032025.pdf")
	amJuly2025 := officialNotice("2025", "AM_2025_35_vom_160725.pdf")
	amFebruary2026 := officialNotice("2026", "AM_2026_05_vom_24022026.pdf")
	amJune2026 := officialNotice("2026", "AM_2026_13_vom_01062026.pdf")
	amJuly2026 := officialNotice("2026", "AM_2026_17_vom_27072026.pdf")
	amSeptember2026 := officialNotice("2026", "AM_2026_24_vom_16092026.pdf")
	amOctober2023 := officialNotice("2023", "Amtsblatt_2023_Nr_27_vom_04-10-2023.pdf")
	base := whsFiles + "Fachbereich-3/informatik/studiengaenge/"
	var result []RegulationSource
	for _, row := range []struct {
		program, degree, code, handbookDate string
		amendments                          []string
	}{
		{"Informatik", "Bachelor", "IN", "2026-10-09", []string{am2024, amMarch2025, amJuly2025, amFebruary2026, amSeptember2026}},
		{"Wirtschaftsinformatik", "Bachelor", "WI", "2026-10-09", []string{am2024, amMarch2025, amJuly2025, amFebruary2026, amJune2026, amJuly2026, amSeptember2026}},
		{"Informatik", "Master", "IN", "2026-09-22", []string{am2024, amMarch2025, amJune2026, amSeptember2026}},
		{"Internet-Sicherheit", "Master", "IS", "2026-09-22", []string{amOctober2023, am2024, amMarch2025, amJuly2025, amJune2026, amSeptember2026}},
		{"Medieninformatik", "Master", "MI", "2026-09-22", []string{amJune2026, amSeptember2026}},
		{"Wirtschaftsinformatik", "Master", "WI", "2026-09-22", []string{amMarch2025, amJune2026, amSeptember2026}},
	} {
		prefix, suffix, section := "BPO", "BA", "bachelor"
		framework := "studium-2023/bachelor-rpo/"
		if row.degree == "Master" {
			prefix, suffix, section = "MPO", "MA", "master"
			framework = "studium-2023/master/rpo/"
		}
		source := RegulationSource{Program: row.program, Degree: row.degree, Version: "2023",
			PO:        RegulationDocument{Title: "Prüfungsordnung", URL: base + "pruefungsordnungen/PO_2023/" + prefix + "_" + row.code + "_2023.pdf", Date: "2023-05-03"},
			Handbook:  RegulationDocument{Title: "Modulhandbuch", URL: base + "modulhandbuecher/PO_2023/MHB_2023_" + row.code + "_" + suffix + ".pdf", Date: row.handbookDate},
			SourceURL: whsInfo + "studium-2023/" + section + "-pos/", HandbookSourceURL: whsInfo + "studium-2023/studienverlauf-" + section + "/", FrameworkURL: whsInfo + framework}
		for _, url := range row.amendments {
			source.Amendments = append(source.Amendments, RegulationDocument{Title: "Änderungssatzung", URL: url})
		}
		result = append(result, source)
	}
	result = append(result, RegulationSource{Program: "Informatik und Design", Degree: "Bachelor", Version: "2023",
		PO:         RegulationDocument{Title: "Prüfungsordnung", URL: officialNotice("2025", "AM_2025_17_vom_170625.pdf"), Date: "2025-01-08"},
		Handbook:   RegulationDocument{Title: "Modulhandbuch", URL: base + "modulhandbuecher/PO_2023/MHB_2023_ID_BA.pdf", Date: "2026-09-22"},
		Amendments: []RegulationDocument{{Title: "Änderungssatzung", URL: amJuly2025}},
		SourceURL:  whsInfo + "studium-2023/bachelor-pos/", HandbookSourceURL: whsInfo + "studium-2023/studienverlauf-bachelor/", FrameworkURL: whsInfo + "studium-2023/bachelor-rpo/",
		Note: "Die WHS führt diesen Studiengang unter PO 2023. Die verlinkte Prüfungsordnung ist vom 08.01.2025; das Modulhandbuch bezeichnet sich weiterhin als PO 2023."})
	legacy := RegulationSource{Program: "Medieninformatik", Degree: "Bachelor", Version: "2016",
		PO:        RegulationDocument{Title: "Prüfungsordnung", URL: base + "pruefungsordnungen/PO_2016/BPO_Medieninformatik_2016.pdf"},
		Handbook:  RegulationDocument{Title: "Modulhandbuch", URL: base + "modulhandbuecher/PO_2016/2016_MHB_MI_BA.pdf", Date: "2025-09-17"},
		SourceURL: whsInfo + "studium-2016/bpo/", HandbookSourceURL: whsInfo + "studium-2016/studienverlauf-bachelor/", FrameworkURL: whsInfo + "studium-2023/bachelor-rpo/",
		Note:       "Für Medieninformatik Bachelor veröffentlicht die WHS eine Auslaufordnung. Die darin genannten Fristen sind zu beachten.",
		Additional: []RegulationDocument{{Title: "Auslaufordnung", URL: officialNotice("2025", "AM_2025_10_vom_170425.pdf"), Date: "2025-04-17"}}}
	for _, item := range [][2]string{
		{"2018", "AM_2018_Nr.3_vom_07-03-2018.pdf"},
		{"2019", "AM_2019_Nr._2_vom_18.01.2019.pdf"},
		{"2019", "AM_Nr._8_vom_22.03.2019.pdf"},
		{"2020", "AM_2020_Nr._6_vom_13-03-2020.pdf"},
		{"2020", "Am_2020_Nr-19_vom_14-05-2020.pdf"},
		{"2021", "Amtsblatt_2021_Nr._1_vom_21-01-2021.pdf"},
		{"2021", "Amtsblatt_2021_Nr_28_vom_18-08-2021.pdf"},
		{"2025", "AM_2025_22_vom_300625.pdf"},
	} {
		legacy.Amendments = append(legacy.Amendments, RegulationDocument{Title: "Änderungssatzung", URL: officialNotice(item[0], item[1])})
	}
	return append(result, legacy)
}
