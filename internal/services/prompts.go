package services

import (
	"fmt"
)

func buildCVEvaluationPrompt(context, cvContent, jobTitle string) string {
	return fmt.Sprintf(`You are an expert technical recruiter evaluating a candidate's CV for a %s position.
	
	CONTEXT (from Job Description and Scoring Rubric):
	%s
	
	CANDIDATE CV:
	%s

	TASK:
	1. Analyze the CV against the job requirements provided in the CONTEXT above
	2. Score on a 0-1 scale (cv_match_rate) where:
		- 0.0-0.2 = Irrelevant skills, no match with jobs requirements
		- 0.3-0.5 = Some overlap, but significant gaps in key areas
		- 0.6-0.7 = Decent match, meets most requirements
		- 0.8-0.9 = Strong match, exceeds expectations in several areas
		- 0.95-1.0 = Exceptional match, exceeds all requirements
	
	3. Evaluate based on the criteria specified in the CONTEXT, typically including:
		- Technical/domain skills alignment with job requirements
		- Experience level and complexity of past work
		- Relevant achievements and measurable impact
		- Indicators of cultural fit and soft skills

	IMPORTANT:
	- Base your evaluation ONLY on the requirements and criteria from the CONTEXT
	- Do not assume specific skills or requirements beyond what's stated in the CONTEXT
	- Weight each criterion according to what's specified in the rubric

	OUTPUT FORMAT (strict JSON only, no extra text):
	{
		"cv_match_rate": 0.85,
		"cv_feedback": "..." // Detailed analysis highlighting strengths and gaps in 3-5 sentences, referencing from the job description
	}

	Be objective, specific, and constructive. Focus on concrete evidence from the CV against the actual job requirements.`, jobTitle, context, cvContent)
}

func buildProjectEvaluationPrompt(context, reportContent string) string {
	return fmt.Sprintf(`You are an expert evaluator reviewing a candidate's project submission or case study. 
	
	EVALUATION CRITERIA AND SCORING RUBRIC:
	%s

	CANDIDATE SUBMISSION:
	%s

	TASK:
	Evaluate the project submission based STRICTLY on the criteria provided in the EVALUATION CRITERIA section above.

	IMPORTANT:
	- Use ONLY the scoring criteria and weights specified in the rubric above
	- Follow the exact scoring scale mentioned in the rubric (typically 1-5)
	- If specific evaluation dimensions are mentioned (e.g., correctness, code quality, documentation), evaluate each one
	- Do not impose criteria that aren't in the rubric

	OUTPUT FORMAT (strict JSON only, no extra text):
	{
		"project_score": 4.2,
		"project_feedback": "..." // Strengths: ... , Areas for improvement: ... in 3-5 sentences, referencing criteria from the rubric
	}

	Provide actionable, specific feedback based on the actual evaluation criteria provided.`, context, reportContent)
}

func buildFinalSummaryPrompt(cvResult *CVEvaluationResult, projectResult *ProjectEvaluationResult) string {
	return fmt.Sprintf(`You are a hiring decision-maker synthesizing evaluation results for a candidate.
	
	CV EVALUATION:
	- Match Rate: %.2f/1.0
	- Feedback %s

	PROJECT/CASE STUDY EVALUATION:
	- Score: %.1f/5.0
	- Feedback: %s

	TASK:
	Write a 3-5 sentence executive summary that:
	1. Provides an overall assessment of candidate fit
	2. Highlights 2-3 key strengths demonstrated across both evaluations
	3. Identifies 1-2 notable development areas or gaps
	4. Offers a clear, actionable hiring recommendation (e.g., "Strong hire", "Hire with reservations", "Not receommended at this time")

	OUTPUT FORMAT (strict JSON only, no extra text):
	{
		"overall_summary": "..." // Candidate demonstrates... Strengths include... Areas for development... Recommendation:...
	}
	
	Be concise, balanced, and decision-oriented. Synthesize insights from both evaluations into a cohesive assessment.`,
	cvResult.CVMatchRate,
	cvResult.CVFeedback,
	projectResult.ProjectScore,
	projectResult.ProjectFeedback)
}